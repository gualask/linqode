package host

// The one platform that cannot be read the way every other one is.
//
// Everything else here is a shell command and a parser, and that holds for
// the local target too: on Linux the local path runs the same batch as the
// remote one, through the same Executor, and produces the same numbers. It
// is not an optimisation opportunity — two implementations that must agree
// eventually stop agreeing, and only one of them would be exercised by the
// e2e fixture.
//
// macOS has no /proc, and no CLI that makes up for it. That is the whole
// justification, and it is narrower than "macOS is different": what is
// missing is not a file but a *counter*. `sysctl kern.cp_time` does not
// exist there; `iostat -c 2` blocks for a second and `top -l 2` for
// two-thirds of one, and both then report percentages rather than the
// cumulative ticks the client-side arithmetic in usage.go subtracts. Read
// natively they are there — user=87320.9 system=39572.2 idle=2762182.4 —
// in exactly the shape that arithmetic already consumes.
//
// So this file exists, gopsutil with it, and both are confined to a build
// tag: the file compiles on Darwin only, the remote path never reaches it,
// and a Mac reached over SSH does not either — the composition root asks for
// this only when the target is this machine, because a native reader asked
// about a remote host would answer about the wrong one.

import (
	"context"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/sensors"

	gohost "github.com/shirou/gopsutil/v4/host"
)

// Sample reads this machine. It is the native counterpart of Command plus
// Parse, and returns the same type, so nothing above it knows which of the
// two produced the numbers.
//
// A reading that fails is left at zero rather than failing the sample, which
// is the rule the parser already follows: the screen declines to draw what
// it has no number for, and a machine that answered six questions out of
// eight is worth showing.
func Sample(ctx context.Context) (Metrics, error) {
	var metrics Metrics
	if average, err := load.AvgWithContext(ctx); err == nil {
		metrics.Load1, metrics.Load5, metrics.Load15 =
			average.Load1, average.Load5, average.Load15
	}
	if count, err := cpu.CountsWithContext(ctx, true); err == nil {
		metrics.CPUs = count
	}
	// The uptime is what the rate arithmetic measures against. gopsutil
	// reports it in whole seconds, which would quantise every rate on a
	// five-second cadence — so it is derived from the boot time instead. The
	// error in a boot time rounded to the second is constant, and a constant
	// cancels in the subtraction of two samples.
	if boot, err := gohost.BootTimeWithContext(ctx); err == nil && boot > 0 {
		since := time.Since(time.Unix(int64(boot), 0))
		metrics.Uptime, metrics.UptimeSeconds = since, since.Seconds()
	}
	if virtual, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		metrics.MemTotalKB = virtual.Total / 1024
		metrics.MemAvailableKB = virtual.Available / 1024
	}
	if swap, err := mem.SwapMemoryWithContext(ctx); err == nil {
		metrics.SwapTotalKB = swap.Total / 1024
		metrics.SwapFreeKB = swap.Free / 1024
	}
	if root, err := disk.UsageWithContext(ctx, "/"); err == nil {
		metrics.DiskTotalKB = root.Total / 1024
		metrics.DiskUsedKB = root.Used / 1024
	}
	metrics.CPUTimes = nativeCPUTimes(ctx)
	metrics.Filesystems = nativeFilesystems(ctx)
	metrics.Interfaces = nativeInterfaces(ctx)
	metrics.Sensors = nativeSensorsFrom(nativeTemperatures(ctx))
	// Pressure stays absent: PSI is a Linux kernel concept, and there is
	// nothing here to report it with. Present is false and nothing is drawn.
	return metrics, nil
}

// nativeCPUTimes is the machine first, then one entry per core, in the shape
// /proc/stat would have given. The unit is milliseconds rather than the
// kernel's jiffies, which changes nothing: every consumer subtracts two
// samples and divides one by the other.
func nativeCPUTimes(ctx context.Context) []CPUTime {
	whole, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return nil
	}
	perCore, _ := cpu.TimesWithContext(ctx, true)
	times := make([]CPUTime, 0, len(whole)+len(perCore))
	for index, entry := range whole {
		name := "cpu"
		if index > 0 {
			// gopsutil returns one aggregate; anything else is unexpected
			// and is kept rather than dropped, named so it cannot collide.
			name = "cpu-aggregate" + strings.Repeat("+", index)
		}
		times = append(times, cpuTimeOf(name, entry))
	}
	for index, entry := range perCore {
		times = append(times, cpuTimeOf("cpu"+itoa(index), entry))
	}
	return times
}

func cpuTimeOf(name string, times cpu.TimesStat) CPUTime {
	return nativeCPUTime(name, nativeTicks{
		User: times.User, System: times.System, Idle: times.Idle,
		Nice: times.Nice, Iowait: times.Iowait, Irq: times.Irq,
		Softirq: times.Softirq, Steal: times.Steal,
	})
}

// nativeFilesystems asks statfs about every mount and then applies the same
// filter the df output goes through, so a Mac drops the six APFS system
// volumes exactly as it does on the command path.
//
// One risk carries over from that path and is not made worse here: a mount
// whose server has gone away can block in statfs. The remote batch guards
// its df with `timeout` where the host has one, and macOS does not ship one,
// so both paths on this platform have the same exposure.
func nativeFilesystems(ctx context.Context) []Filesystem {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	mounts := make(map[string]Filesystem, len(partitions))
	for _, partition := range partitions {
		usage, err := disk.UsageWithContext(ctx, partition.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}
		mounts[partition.Mountpoint] = Filesystem{
			Device:  partition.Device,
			Mount:   partition.Mountpoint,
			TotalKB: usage.Total / 1024,
			UsedKB:  usage.Used / 1024,
		}
	}
	return withoutTheSealedTwin(realFilesystems(mounts))
}

func nativeInterfaces(ctx context.Context) []Interface {
	counters, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil
	}
	interfaces := make([]Interface, 0, len(counters))
	for _, counter := range counters {
		interfaces = append(interfaces, Interface{
			Name:    counter.Name,
			RxBytes: counter.BytesRecv,
			TxBytes: counter.BytesSent,
		})
	}
	return interfaces
}

// nativeTemperatures is the translation half of the sensor reading: what
// gopsutil hands over, in the shape the platform-neutral half consumes.
func nativeTemperatures(ctx context.Context) []nativeReading {
	readings, err := sensors.TemperaturesWithContext(ctx)
	if err != nil && len(readings) == 0 {
		return nil
	}
	native := make([]nativeReading, 0, len(readings))
	for _, reading := range readings {
		native = append(native, nativeReading{Key: reading.SensorKey,
			Celsius: reading.Temperature, High: reading.High, Critical: reading.Critical})
	}
	return native
}

// SampleProcesses reads what this machine is running. The counters are the
// same two the /proc path collects — CPU time since the process started, and
// resident memory — so the same client-side subtraction turns them into a
// percentage.
//
// ClockTck is 1000 because the times below are milliseconds, and
// UptimeSeconds is the clock they are measured against, exactly as on the
// other path.
func SampleProcesses(ctx context.Context) (ProcessSample, error) {
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return ProcessSample{}, err
	}
	sample := ProcessSample{ClockTck: 1000}
	if boot, err := gohost.BootTimeWithContext(ctx); err == nil && boot > 0 {
		sample.UptimeSeconds = time.Since(time.Unix(int64(boot), 0)).Seconds()
	}
	sample.Processes = make([]Process, 0, len(processes))
	for _, running := range processes {
		// A process that exited between the listing and the read simply is
		// not there, which is what the /proc path says about the same race.
		name, err := running.NameWithContext(ctx)
		if err != nil || name == "" {
			continue
		}
		entry := Process{PID: int(running.Pid), Name: name}
		if memory, err := running.MemoryInfoWithContext(ctx); err == nil && memory != nil {
			entry.RSSKB = memory.RSS / 1024
		}
		if times, err := running.TimesWithContext(ctx); err == nil && times != nil {
			entry.CPUTicks = uint64((times.User + times.System) * 1000)
		}
		if threads, err := running.NumThreadsWithContext(ctx); err == nil {
			entry.Threads = int(threads)
		}
		// State is deliberately not read. It is one field of
		// /proc/<pid>/stat on the other path and free there; here it shells
		// out to `ps` once per process — measured at **2.0 s** for 684
		// processes, against 28 ms for everything else in this loop
		// together. Nothing on the screen shows it.
		sample.Processes = append(sample.Processes, entry)
	}
	return sample, nil
}

// itoa keeps the core names out of fmt for a function called once per core
// per sample.
func itoa(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return itoa(value/10) + string(rune('0'+value%10))
}

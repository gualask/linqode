package home

import (
	"fmt"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
)

// busyHost is one host sample. round advances it: /proc/stat and
// /proc/net/dev are counters, so the frames only show a CPU percentage or a
// throughput once two rounds have gone by — the same wait a real session
// has, and the reason the band draws load until then.
//
// The shape is a machine worth looking at: one core pinned while the average
// says forty percent, a /var about to fill, and swap in use.
func busyHost(round int) host.Metrics {
	const secondsPerRound = 5
	// Per core, per round: five seconds at a hundred jiffies a second.
	const jiffies = secondsPerRound * 100

	m := host.Metrics{
		Load1: 7.21, Load5: 5.98, Load15: 4.55, CPUs: 8,
		Uptime:         42*24*time.Hour + 7*time.Hour,
		UptimeSeconds:  3654000 + float64(round*secondsPerRound),
		MemTotalKB:     32833536,
		MemAvailableKB: 3923456,
		SwapTotalKB:    8388608,
		SwapFreeKB:     5033165,
		DiskTotalKB:    205520896,
		DiskUsedKB:     93323264,
		Filesystems: []host.Filesystem{
			{Device: "/dev/nvme0n1p2", Mount: "/", TotalKB: 205520896, UsedKB: 93323264},
			{Device: "/dev/nvme0n1p1", Mount: "/boot", TotalKB: 1046528, UsedKB: 314572},
			{Device: "/dev/sdb1", Mount: "/var", TotalKB: 419430400, UsedKB: 390455296},
		},
		Pressure: host.PressureSet{
			CPU:     host.Pressure{Some10: 24.5},
			IO:      host.Pressure{Some10: 8.3, Full10: 2.1},
			Present: true,
		},
		// A machine whose drive is closer to its own limit than its CPU is
		// to that one — which is the case the share exists to get right,
		// and which the bare numbers get backwards.
		Sensors: []host.Sensor{
			{Chip: "nvme", Label: "Composite", MilliC: 74_000, LimitMilliC: 84_850},
			{Chip: "coretemp", Label: "Package id 0", MilliC: 71_000, LimitMilliC: 100_000},
			{Chip: "acpitz", MilliC: 44_000},
		},
	}
	// Memory climbing towards trouble rather than sitting there, which is
	// the difference the trend beside it exists to show.
	m.MemAvailableKB = 12_000_000 - uint64(round)*260_000

	// One core pinned, one busy, the rest idling — which is the case a load
	// average of 7.21 over eight cores cannot distinguish from six cores at
	// nine tenths.
	busy := []float64{0.98, 0.61, 0.12, 0.09, 0.31, 0.04, 0.07, 0.02}
	machine := host.CPUTime{Name: "cpu"}
	var traffic uint64
	for core, fraction := range busy {
		var counter host.CPUTime
		// The counters are cumulative, so each round is added rather than
		// multiplied in: that is what gives the strip a shape instead of a
		// flat line at one value.
		for r := 1; r <= round; r++ {
			counter.Total += jiffies
			counter.Idle += uint64(float64(jiffies) * (1 - fraction*shotWave(r)))
		}
		counter.Name = fmt.Sprintf("cpu%d", core)
		m.CPUTimes = append(m.CPUTimes, counter)
		machine.Total += counter.Total
		machine.Idle += counter.Idle
	}
	m.CPUTimes = append([]host.CPUTime{machine}, m.CPUTimes...)
	for r := 1; r <= round; r++ {
		traffic += uint64(1_450_000 * shotWave(r))
	}
	m.Interfaces = []host.Interface{
		{Name: "lo", RxBytes: uint64(round) * 12_000, TxBytes: uint64(round) * 12_000},
		{Name: "eth0", RxBytes: traffic, TxBytes: traffic / 4},
		{Name: "veth3f1a", RxBytes: uint64(round) * 980_000, TxBytes: uint64(round) * 210_000},
	}
	return m
}

// shotWave is a deterministic stand-in for a machine doing something: the
// per-round multiplier that gives the sparklines a shape to be read.
func shotWave(round int) float64 {
	pattern := []float64{0.22, 0.35, 0.50, 0.78, 1.00, 0.92, 0.61, 0.40,
		0.28, 0.44, 0.70, 0.96, 0.52, 0.30}
	return pattern[(round-1)%len(pattern)]
}

// Every state the table has a color for, in one project.
func shotServices() []compose.Service {
	count := func(n int) *int { return &n }
	return []compose.Service{
		{Service: "nginx", Name: "myapp-nginx-1", ID: "id-nginx", Pid: 100, Project: "myapp",
			State: "running", Health: "healthy",
			Status: "Up 3 days (healthy)", Restarts: count(0),
			Publishers: []compose.Publisher{{PublishedPort: 443, TargetPort: 443, Protocol: "tcp"}}},
		{Service: "api", Name: "myapp-api-1", ID: "id-api", Pid: 101, Project: "myapp",
			State: "running", Health: "healthy",
			Status: "Up 3 days (healthy)", Restarts: count(2),
			Publishers: []compose.Publisher{{PublishedPort: 8080, TargetPort: 3000, Protocol: "tcp"}}},
		{Service: "postgres", Name: "myapp-postgres-1", ID: "id-postgres", Pid: 102, Project: "myapp",
			State: "running", Health: "starting",
			Status: "Up 4 seconds (health: starting)", Restarts: count(0)},
		{Service: "cache", Name: "myapp-cache-1", ID: "id-cache", Pid: 103, Project: "myapp",
			State: "running", Health: "unhealthy",
			Status: "Up 2 hours (unhealthy)", Restarts: count(0)},
		{Service: "migrate", Name: "myapp-migrate-1", Project: "myapp", State: "exited",
			Status: "Exited (0) 3 days ago", Restarts: count(0)},
		{Service: "worker", Name: "myapp-worker-1", Project: "myapp", State: "restarting",
			Status: "Restarting (137) 12 seconds ago", Restarts: count(7)},
	}
}

// shotCgroups is a pair of readings a few seconds apart, so the frames show
// what the second one derives: a CPU percentage per container, memory against
// the machine's, and the I/O totals.
func shotCgroups(round int) compose.CgroupSample {
	// Microseconds of CPU per round. Five seconds pass between rounds, so
	// each value is the percentage the frame should show times fifty
	// thousand: 5_617_000 reads as 112.34%, an api using more than a core.
	busy := map[string]uint64{"nginx": 7_500, "api": 5_617_000, "postgres": 151_000,
		"cache": 4_405_000}
	memory := map[string]uint64{"nginx": 12_939_428, "api": 1_325_142_016,
		"postgres": 886_244_147, "cache": 31_030_896_230}
	sample := compose.CgroupSample{
		At:         time.Date(2026, 9, 4, 12, 0, round*5, 0, time.UTC),
		Containers: map[string]compose.CgroupReading{},
		Networks:   map[int]compose.CgroupReading{},
	}
	for index, service := range []string{"nginx", "api", "postgres", "cache"} {
		sample.Containers["id-"+service] = compose.CgroupReading{
			CPUMicros:  busy[service] * uint64(round),
			MemBytes:   memory[service],
			ReadBytes:  4_100 + uint64(index)*1_230_000_000,
			WriteBytes: uint64(index) * 4_560_000_000,
			PIDs:       14,
		}
		sample.Networks[100+index] = compose.CgroupReading{
			RxBytes: 1_450_000_000 >> (index * 3), TxBytes: 892_300_000 >> (index * 2)}
	}
	return sample
}

// busyProcesses is the process table of the same machine, advancing with the
// round so the second reading has CPU shares on it. The shape is the one
// worth opening the view for: a database holding most of the memory while
// something else is burning a core and a half.
func busyProcesses(round int) host.ProcessSample {
	const secondsPerRound = 5
	sample := host.ProcessSample{
		UptimeSeconds: 3654000 + float64(round*secondsPerRound),
		ClockTck:      100,
	}
	// name, RSS in KB, and ticks of CPU per round — 500 is one full core.
	table := []struct {
		pid   int
		name  string
		rssKB uint64
		ticks uint64
	}{
		{9821, "postgres", 11_534_336, 40},
		{1204, "dockerd", 1_048_576, 60},
		{9902, "ffmpeg", 262_144, 750},
		{2033, "node", 1_887_437, 120},
		{9877, "redis-server", 98_304, 15},
		{411, "containerd", 45_875, 25},
		{9955, "nginx", 12_288, 3},
		{25, "sshd", 3_120, 1},
		{1, "systemd", 384, 0},
	}
	for _, entry := range table {
		sample.Processes = append(sample.Processes, host.Process{
			PID: entry.pid, Name: entry.name, State: "S", Threads: 4,
			RSSKB: entry.rssKB, CPUTicks: entry.ticks * uint64(round),
		})
	}
	return sample
}

// shotEvents is a minute in the life of a deployment going wrong, oldest
// first: the worker runs out of memory, is killed, comes back, and the cache
// fails its health check. Every colour the feed can draw is in here.
func shotEvents() []compose.Event {
	start := time.Date(2026, 9, 5, 12, 3, 14, 0, time.UTC)
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	return []compose.Event{
		{At: at(0), Action: "health_status: healthy", Container: "myapp-api-1"},
		{At: at(19), Action: "oom", Container: "myapp-worker-1"},
		{At: at(19), Action: "die", Container: "myapp-worker-1", ExitCode: "137"},
		{At: at(21), Action: "start", Container: "myapp-worker-1"},
		{At: at(38), Action: "health_status: unhealthy", Container: "myapp-cache-1"},
		{At: at(44), Action: "die", Container: "myapp-migrate-1", ExitCode: "0"},
	}
}

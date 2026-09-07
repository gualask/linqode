// Package host collects resource metrics of the remote machine — CPU,
// load, memory, swap, filesystems, network, pressure, uptime — for the
// meter band and the system view behind it.
//
// It reads /proc and POSIX `df -Pk` rather than parsing uptime(1) or
// free(1), whose output formats differ between distributions, busybox, and
// versions. The whole thing is one remote command: adding a reading to it
// costs no extra round-trip, which is why it reads as much as it does.
// Measured at ~2 ms against the e2e fixture, against ~60 ms for the
// `compose ps` already in the refresh cycle (see docs/PROJECT.md).
//
// Two of the readings here are counters, not values: /proc/stat counts the
// jiffies a CPU has spent since boot, and /proc/net/dev the bytes an
// interface has carried. A percentage and a rate are the difference between
// two samples, which Since computes — the same client-side arithmetic the
// container counters use, and for the same reason: the server is never
// asked to sit and wait a second on our behalf.
package host

import "time"

// CPUTime is one line of /proc/stat: how many jiffies a CPU has spent doing
// something since boot, and how many it has spent idle. Neither number
// means anything alone; their difference across two samples is the
// percentage.
type CPUTime struct {
	// Name is "cpu" for the machine as a whole, "cpu0", "cpu1"… per core.
	Name  string
	Total uint64
	Idle  uint64
}

// Interface is one line of /proc/net/dev: bytes carried since boot.
type Interface struct {
	Name             string
	RxBytes, TxBytes uint64
}

// Pressure is one /proc/pressure file: the share of the last ten seconds in
// which at least one task was stalled waiting for the resource (Some), and
// the share in which every task was (Full). Full is what a machine in real
// trouble reports; on cpu the kernel does not report it at all.
//
// It is a better answer than load average to "is this machine suffering":
// load counts runnable tasks, which says nothing about whether they are
// waiting on disk or on reclaim.
type Pressure struct {
	Some10 float64
	Full10 float64
}

// PressureSet is the three pressure files. Present is false on a kernel
// without PSI, or one that needs psi=1 at boot — which is several
// Debian and Ubuntu builds — and then nothing is drawn rather than zeros.
type PressureSet struct {
	CPU, IO, Memory Pressure
	Present         bool
}

// Metrics is one sample of the remote machine's resource usage. A field
// left at zero means the host did not report it (an unreadable /proc entry,
// a df that failed); the view renders those as unknown rather than as a
// real zero, since none of these is plausibly zero on a live machine.
type Metrics struct {
	Load1, Load5, Load15 float64
	CPUs                 int
	Uptime               time.Duration
	// UptimeSeconds is the same reading undivided. It is the clock the
	// rates are computed against: the host's own, so a slow or jittery link
	// cannot stretch a second into a spike.
	UptimeSeconds  float64
	MemTotalKB     uint64
	MemAvailableKB uint64
	SwapTotalKB    uint64
	SwapFreeKB     uint64
	// DiskTotalKB and DiskUsedKB are the root filesystem, read on its own so
	// the one reading that is always wanted never depends on the full mount
	// list arriving.
	DiskTotalKB uint64
	DiskUsedKB  uint64
	// CPUTimes is the machine first, then one entry per core.
	CPUTimes    []CPUTime
	Filesystems []Filesystem
	Interfaces  []Interface
	Pressure    PressureSet
	// Sensors is one temperature per chip, closest to its own limit first.
	// Empty on the many hosts that report none — most virtual machines, and
	// the e2e fixture.
	Sensors []Sensor
}

// MemUsedKB is memory in use: what the kernel reports as unavailable to new
// allocations, which is the number an operator cares about (not "free",
// which excludes reclaimable cache).
func (m Metrics) MemUsedKB() uint64 {
	if m.MemTotalKB == 0 || m.MemAvailableKB > m.MemTotalKB {
		return 0
	}
	return m.MemTotalKB - m.MemAvailableKB
}

// MemUsedPercent is 0 when memory was not reported.
func (m Metrics) MemUsedPercent() float64 {
	if m.MemTotalKB == 0 {
		return 0
	}
	return float64(m.MemUsedKB()) / float64(m.MemTotalKB) * 100
}

// SwapUsedKB is swap in use. A machine with no swap configured reports a
// total of zero, which is not the same as unused swap and must not be drawn
// as an empty meter.
func (m Metrics) SwapUsedKB() uint64 {
	if m.SwapTotalKB == 0 || m.SwapFreeKB > m.SwapTotalKB {
		return 0
	}
	return m.SwapTotalKB - m.SwapFreeKB
}

// SwapUsedPercent is 0 when there is no swap.
func (m Metrics) SwapUsedPercent() float64 {
	if m.SwapTotalKB == 0 {
		return 0
	}
	return float64(m.SwapUsedKB()) / float64(m.SwapTotalKB) * 100
}

// LoadPerCPU normalizes the 1-minute load by core count, so 1.0 means
// "fully busy" on any machine. It is 0 when either number is missing.
func (m Metrics) LoadPerCPU() float64 {
	if m.CPUs == 0 {
		return 0
	}
	return m.Load1 / float64(m.CPUs)
}

// HasLoad reports whether the load average was read; zero load is possible
// on an idle machine, so it cannot be inferred from the value alone.
func (m Metrics) HasLoad() bool { return m.CPUs > 0 }

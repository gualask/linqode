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

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Section markers keep the parser independent of how each tool orders or
// labels its fields.
const (
	loadMarker     = "#load"
	uptimeMarker   = "#uptime"
	memMarker      = "#mem"
	cpuMarker      = "#cpu"
	statMarker     = "#stat"
	netMarker      = "#net"
	pressureMarker = "#pressure"
	diskMarker     = "#disk"
	mountsMarker   = "#mounts"
)

// mountsCommand lists every mounted filesystem. It is separate from the
// root one, and guarded, for a reason that has nothing to do with cost: a
// df with no argument calls statfs on every mount, and a hung network mount
// makes it block for as long as the kernel's timeout allows. `timeout` is
// not POSIX, so its absence must not lose the reading — and the root
// filesystem, the one reading that is always wanted, is read by its own df
// above and never depends on this one finishing.
const mountsCommand = "if command -v timeout >/dev/null 2>&1; " +
	"then timeout 5 df -Pk 2>/dev/null; else df -Pk 2>/dev/null; fi"

// Command is the remote command producing all metrics in one round-trip.
// `df -Pk` forces POSIX output in 1024-byte blocks, which is the one form
// every df agrees on, and one line per filesystem however long its device
// name is.
func Command() string {
	return "echo '" + loadMarker + "'; cat /proc/loadavg; " +
		"echo '" + uptimeMarker + "'; cat /proc/uptime; " +
		"echo '" + memMarker + "'; grep -E '^(MemTotal|MemAvailable|SwapTotal|SwapFree):' /proc/meminfo; " +
		"echo '" + cpuMarker + "'; grep -c '^processor' /proc/cpuinfo; " +
		"echo '" + statMarker + "'; grep '^cpu' /proc/stat; " +
		"echo '" + netMarker + "'; cat /proc/net/dev; " +
		"echo '" + pressureMarker + "'; grep -H '' /proc/pressure/cpu /proc/pressure/io /proc/pressure/memory 2>/dev/null; " +
		"echo '" + thermalMarker + "'; " + thermalCommand + "; " +
		"echo '" + diskMarker + "'; df -Pk /; " +
		"echo '" + mountsMarker + "'; " + mountsCommand
}

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

// Filesystem is one line of df.
type Filesystem struct {
	Device  string
	Mount   string
	TotalKB uint64
	UsedKB  uint64
}

// UsedPercent is 0 for a filesystem that reports no size — which several
// pseudo-filesystems do, and which is one reason they are dropped.
func (f Filesystem) UsedPercent() float64 {
	if f.TotalKB == 0 {
		return 0
	}
	return float64(f.UsedKB) / float64(f.TotalKB) * 100
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

// Parse reads the output of Command. Missing or malformed sections leave
// their fields zero instead of failing the whole sample: a host that does
// not expose one of these is still worth showing the rest of.
func Parse(raw []byte) (Metrics, error) {
	sections := split(string(raw))
	if len(sections) == 0 {
		return Metrics{}, fmt.Errorf("no metrics in host output")
	}

	var m Metrics
	if fields := strings.Fields(sections[loadMarker]); len(fields) >= 3 {
		m.Load1, _ = strconv.ParseFloat(fields[0], 64)
		m.Load5, _ = strconv.ParseFloat(fields[1], 64)
		m.Load15, _ = strconv.ParseFloat(fields[2], 64)
	}
	if fields := strings.Fields(sections[uptimeMarker]); len(fields) >= 1 {
		if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
			m.UptimeSeconds = seconds
			m.Uptime = time.Duration(seconds) * time.Second
		}
	}
	for line := range strings.Lines(sections[memMarker]) {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		amount := strings.Fields(value)
		if len(amount) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(amount[0], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "MemTotal":
			m.MemTotalKB = kb
		case "MemAvailable":
			m.MemAvailableKB = kb
		case "SwapTotal":
			m.SwapTotalKB = kb
		case "SwapFree":
			m.SwapFreeKB = kb
		}
	}
	if fields := strings.Fields(sections[cpuMarker]); len(fields) >= 1 {
		m.CPUs, _ = strconv.Atoi(fields[0])
	}
	m.CPUTimes = parseCPUTimes(sections[statMarker])
	m.Interfaces = parseInterfaces(sections[netMarker])
	m.Pressure = parsePressure(sections[pressureMarker])
	m.Sensors = parseThermal(sections[thermalMarker])
	if root, ok := parseDF(sections[diskMarker])["/"]; ok {
		m.DiskTotalKB, m.DiskUsedKB = root.TotalKB, root.UsedKB
	}
	m.Filesystems = realFilesystems(parseDF(sections[mountsMarker]))
	if m.DiskTotalKB == 0 {
		// The root df failed but the mount list did not, which is the shape
		// a `timeout` that is not there would have left behind if the two
		// were one command.
		for _, filesystem := range m.Filesystems {
			if filesystem.Mount == "/" {
				m.DiskTotalKB, m.DiskUsedKB = filesystem.TotalKB, filesystem.UsedKB
			}
		}
	}
	return m, nil
}

// parseCPUTimes reads the `cpu` lines of /proc/stat. The fields are jiffies
// spent in user, nice, system, idle, iowait, irq, softirq and steal; the
// two that follow (guest, guest_nice) are already counted inside user and
// nice, so summing them would inflate the total and shrink every
// percentage.
func parseCPUTimes(section string) []CPUTime {
	const lastCounted = 8 // steal, the last field that is not double counted
	var times []CPUTime
	for line := range strings.Lines(section) {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		entry := CPUTime{Name: fields[0]}
		for index := 1; index < len(fields) && index <= lastCounted; index++ {
			value, err := strconv.ParseUint(fields[index], 10, 64)
			if err != nil {
				break
			}
			entry.Total += value
			// idle is the fourth field, iowait the fifth: both are time the
			// CPU had nothing to run, whatever the reason.
			if index == 4 || index == 5 {
				entry.Idle += value
			}
		}
		if entry.Total > 0 {
			times = append(times, entry)
		}
	}
	return times
}

// parseInterfaces reads /proc/net/dev, whose two header lines carry no
// colon and are skipped by the same rule that finds the interface name.
func parseInterfaces(section string) []Interface {
	const txBytesField = 8 // after the eight receive columns
	var interfaces []Interface
	for line := range strings.Lines(section) {
		name, counters, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(counters)
		if len(fields) <= txBytesField {
			continue
		}
		rx, errRx := strconv.ParseUint(fields[0], 10, 64)
		tx, errTx := strconv.ParseUint(fields[txBytesField], 10, 64)
		if errRx != nil || errTx != nil {
			continue
		}
		interfaces = append(interfaces, Interface{
			Name: strings.TrimSpace(name), RxBytes: rx, TxBytes: tx})
	}
	return interfaces
}

// parsePressure reads the grep -H output of the three pressure files, whose
// lines are `<path>:some avg10=0.00 avg60=0.00 avg300=0.00 total=0`.
func parsePressure(section string) PressureSet {
	var set PressureSet
	for line := range strings.Lines(section) {
		path, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}
		_, value, ok := strings.Cut(fields[1], "=")
		if !ok {
			continue
		}
		average, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}
		var target *Pressure
		switch {
		case strings.HasSuffix(path, "/cpu"):
			target = &set.CPU
		case strings.HasSuffix(path, "/io"):
			target = &set.IO
		case strings.HasSuffix(path, "/memory"):
			target = &set.Memory
		default:
			continue
		}
		switch fields[0] {
		case "some":
			target.Some10 = average
		case "full":
			target.Full10 = average
		default:
			continue
		}
		set.Present = true
	}
	return set
}

// parseDF reads df -Pk output, keyed by mount point. df prints a header
// line, then one line per filesystem. Fields: device, 1024-blocks, used,
// available, capacity, mountpoint — and -P guarantees they are on one line,
// however long the device name is. A mount point containing spaces is
// rejoined, since it is always the last column.
func parseDF(section string) map[string]Filesystem {
	found := map[string]Filesystem{}
	for line := range strings.Lines(section) {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[1] == "1024-blocks" {
			continue
		}
		total, errTotal := strconv.ParseUint(fields[1], 10, 64)
		used, errUsed := strconv.ParseUint(fields[2], 10, 64)
		if errTotal != nil || errUsed != nil {
			continue
		}
		mount := strings.Join(fields[5:], " ")
		found[mount] = Filesystem{
			Device: fields[0], Mount: mount, TotalKB: total, UsedKB: used}
	}
	return found
}

// pseudoDevices are the filesystems that are not storage: listing them
// would bury the two or three mounts an operator actually watches under a
// screenful of tmpfs.
var pseudoDevices = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "ramfs": true, "shm": true,
	"overlay": true, "none": true, "udev": true, "efivarfs": true,
	"proc": true, "sysfs": true, "devpts": true, "mqueue": true,
	"cgroup": true, "cgroup2": true, "hugetlbfs": true, "squashfs": true,
}

// systemMounts are the trees whose contents are the kernel's or the
// daemon's bookkeeping rather than a disk anyone provisioned. Everything
// under /var/lib/docker is the per-container overlay mounts: one or more
// per running container, all of them views of a filesystem that is already
// in the list under its own mount point. /etc is there for the same reason
// from the other side — nothing under it is ever a filesystem in its own
// right, and what df finds there inside a container is the bind-mounted
// resolv.conf and hosts, reporting the numbers of the disk behind them.
var systemMounts = []string{
	"/proc", "/sys", "/dev", "/run", "/snap", "/etc", "/var/lib/docker",
}

// realFilesystems is the mount list worth showing, sorted by mount point.
//
// The root filesystem is kept unconditionally. On a containerised host —
// which the e2e fixture is, and which a good number of real targets are —
// `/` is itself an overlay, and a rule that dropped pseudo-filesystems
// without that exception would drop the one mount that matters most.
func realFilesystems(mounts map[string]Filesystem) []Filesystem {
	kept := make([]Filesystem, 0, len(mounts))
	for mount, filesystem := range mounts {
		if mount != "/" && !keepMount(filesystem) {
			continue
		}
		kept = append(kept, filesystem)
	}
	// A bind mount reports the same device and the same numbers under a
	// second path. Only the shortest path is kept, which is the one the
	// filesystem is actually mounted at.
	deduped := kept[:0]
	for _, filesystem := range kept {
		duplicate := false
		for index, seen := range deduped {
			if seen.Device != filesystem.Device || seen.TotalKB != filesystem.TotalKB ||
				seen.UsedKB != filesystem.UsedKB {
				continue
			}
			duplicate = true
			if len(filesystem.Mount) < len(seen.Mount) {
				deduped[index] = filesystem
			}
			break
		}
		if !duplicate {
			deduped = append(deduped, filesystem)
		}
	}
	// By mount point, which puts "/" first: no other path sorts before a
	// bare slash.
	if len(deduped) == 0 {
		return nil
	}
	slices.SortFunc(deduped, func(a, b Filesystem) int {
		return strings.Compare(a.Mount, b.Mount)
	})
	return deduped
}

// keepMount is whether a filesystem other than the root is worth a row.
func keepMount(filesystem Filesystem) bool {
	if filesystem.TotalKB == 0 || pseudoDevices[filesystem.Device] {
		return false
	}
	for _, prefix := range systemMounts {
		if filesystem.Mount == prefix || strings.HasPrefix(filesystem.Mount, prefix+"/") {
			return false
		}
	}
	return true
}

// split cuts the output into its marked sections.
func split(raw string) map[string]string {
	sections := map[string]string{}
	current := ""
	var body strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = body.String()
		}
		body.Reset()
	}
	for line := range strings.Lines(raw) {
		if marker := strings.TrimSpace(line); strings.HasPrefix(marker, "#") {
			flush()
			current = marker
			continue
		}
		body.WriteString(line)
	}
	flush()
	return sections
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

// DiskUsedPercent is 0 when the root filesystem was not reported.
func (m Metrics) DiskUsedPercent() float64 {
	if m.DiskTotalKB == 0 {
		return 0
	}
	return float64(m.DiskUsedKB) / float64(m.DiskTotalKB) * 100
}

// Fullest is the filesystem closest to full — the one worth the band's
// single disk meter. A root that is comfortable says nothing about a
// /var/lib/docker that is not, and a full one is among the most common
// causes of a deployment that stopped working.
func (m Metrics) Fullest() (Filesystem, bool) {
	var fullest Filesystem
	found := false
	for _, filesystem := range m.Filesystems {
		if !found || filesystem.UsedPercent() > fullest.UsedPercent() {
			fullest, found = filesystem, true
		}
	}
	if !found && m.DiskTotalKB > 0 {
		return Filesystem{Mount: "/", TotalKB: m.DiskTotalKB, UsedKB: m.DiskUsedKB}, true
	}
	return fullest, found
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

// Package host collects basic resource metrics of the remote machine —
// load, memory, disk, uptime — for the status view's header.
//
// It reads /proc and POSIX `df -Pk` rather than parsing uptime(1) or
// free(1), whose output formats differ between distributions, busybox, and
// versions. The whole thing is one remote command returning a few hundred
// bytes: measured at ~2 ms against the e2e fixture, against ~60 ms for the
// `compose ps` already in the refresh cycle (see docs/PROJECT.md).
package host

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Section markers keep the parser independent of how each tool orders or
// labels its fields.
const (
	loadMarker   = "#load"
	uptimeMarker = "#uptime"
	memMarker    = "#mem"
	cpuMarker    = "#cpu"
	diskMarker   = "#disk"
)

// Command is the remote command producing all metrics in one round-trip.
// `df -Pk` forces POSIX output in 1024-byte blocks, which is the one form
// every df agrees on.
func Command() string {
	return "echo '" + loadMarker + "'; cat /proc/loadavg; " +
		"echo '" + uptimeMarker + "'; cat /proc/uptime; " +
		"echo '" + memMarker + "'; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo; " +
		"echo '" + cpuMarker + "'; grep -c '^processor' /proc/cpuinfo; " +
		"echo '" + diskMarker + "'; df -Pk /"
}

// Metrics is one sample of the remote machine's resource usage. A field
// left at zero means the host did not report it (an unreadable /proc entry,
// a df that failed); the view renders those as unknown rather than as a
// real zero, since none of these is plausibly zero on a live machine.
type Metrics struct {
	Load1, Load5, Load15 float64
	CPUs                 int
	Uptime               time.Duration
	MemTotalKB           uint64
	MemAvailableKB       uint64
	DiskTotalKB          uint64
	DiskUsedKB           uint64
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
		}
	}
	if fields := strings.Fields(sections[cpuMarker]); len(fields) >= 1 {
		m.CPUs, _ = strconv.Atoi(fields[0])
	}
	// df prints a header line, then one line per filesystem; `/` is the only
	// one asked for. Fields: device, 1024-blocks, used, available, capacity,
	// mountpoint.
	for line := range strings.Lines(sections[diskMarker]) {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[1] == "1024-blocks" {
			continue
		}
		total, errTotal := strconv.ParseUint(fields[1], 10, 64)
		used, errUsed := strconv.ParseUint(fields[2], 10, 64)
		if errTotal == nil && errUsed == nil {
			m.DiskTotalKB, m.DiskUsedKB = total, used
		}
	}
	return m, nil
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

// DiskUsedPercent is 0 when the root filesystem was not reported.
func (m Metrics) DiskUsedPercent() float64 {
	if m.DiskTotalKB == 0 {
		return 0
	}
	return float64(m.DiskUsedKB) / float64(m.DiskTotalKB) * 100
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

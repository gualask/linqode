package host

import "strings"

// Usage is what two samples say that one cannot. /proc/stat and
// /proc/net/dev are counters since boot: a single reading of either is a
// large number that means nothing, and the difference between two is the
// percentage and the rate an operator reads.
//
// This is the same arithmetic the container counters do, done here for the
// same reason: the alternative is asking the server to sample twice and
// sleep in between, which is what makes `docker stats` cost two seconds.
type Usage struct {
	// CPUPercent is the machine, 0–100 whatever the core count.
	CPUPercent float64
	// Cores is one entry per core, in /proc/stat order.
	Cores []float64
	// The machine's traffic: the interfaces that carry it, summed. Bytes
	// per second.
	RxRate, TxRate float64
	Interfaces     []InterfaceRate
}

// InterfaceRate is one interface's throughput, bytes per second.
type InterfaceRate struct {
	Name           string
	RxRate, TxRate float64
}

// Since measures this sample against the one before it. It reports false
// when there is nothing to measure — the first sample after connecting, or
// a pair the host's own clock says did not advance.
//
// The elapsed time comes from /proc/uptime, not from a local clock: it is
// the interval the counters themselves were accumulated over, so a slow
// round-trip or a stalled UI cannot turn a quiet second into a spike.
func (m Metrics) Since(previous Metrics) (Usage, bool) {
	elapsed := m.UptimeSeconds - previous.UptimeSeconds
	if elapsed <= 0 {
		// Either there is no previous sample, or the host rebooted between
		// the two — and after a reboot every counter restarted as well.
		return Usage{}, false
	}

	var usage Usage
	measured := false
	before := make(map[string]CPUTime, len(previous.CPUTimes))
	for _, entry := range previous.CPUTimes {
		before[entry.Name] = entry
	}
	for _, entry := range m.CPUTimes {
		busy, ok := busyPercent(before[entry.Name], entry)
		if !ok {
			continue
		}
		measured = true
		if entry.Name == "cpu" {
			usage.CPUPercent = busy
			continue
		}
		usage.Cores = append(usage.Cores, busy)
	}

	previousBytes := make(map[string]Interface, len(previous.Interfaces))
	for _, entry := range previous.Interfaces {
		previousBytes[entry.Name] = entry
	}
	for _, entry := range m.Interfaces {
		last, ok := previousBytes[entry.Name]
		if !ok {
			// An interface that appeared between the two samples — a
			// container starting is enough — has nothing to be measured
			// against yet.
			continue
		}
		rate := InterfaceRate{
			Name:   entry.Name,
			RxRate: float64(advanced(last.RxBytes, entry.RxBytes)) / elapsed,
			TxRate: float64(advanced(last.TxBytes, entry.TxBytes)) / elapsed,
		}
		measured = true
		usage.Interfaces = append(usage.Interfaces, rate)
		if carriesHostTraffic(entry.Name) {
			usage.RxRate += rate.RxRate
			usage.TxRate += rate.TxRate
		}
	}
	return usage, measured
}

// busyPercent is the share of the jiffies that passed between two readings
// in which the CPU had something to run. It needs no clock: the total
// includes the idle time, so the two counters divide into a percentage on
// their own.
func busyPercent(before, after CPUTime) (float64, bool) {
	total := advanced(before.Total, after.Total)
	if before.Total == 0 || total == 0 {
		return 0, false
	}
	idle := advanced(before.Idle, after.Idle)
	if idle > total {
		return 0, false
	}
	return float64(total-idle) / float64(total) * 100, true
}

// advanced is how far a counter moved. A counter that went backwards was
// reset — a reboot, or an interface recreated under the same name — and
// reports no movement rather than an enormous one.
func advanced(before, after uint64) uint64 {
	if after < before {
		return 0
	}
	return after - before
}

// virtualPrefixes name the interfaces whose bytes are already counted
// somewhere else. Every container's traffic crosses a veth pair and the
// bridge behind it before it reaches the machine's own interface, so
// summing everything would count the same packet three times; loopback is
// not traffic at all, and a tunnel's payload is carried again by whatever
// interface the tunnel runs over.
var virtualPrefixes = []string{
	"veth", "docker", "br-", "virbr", "cni", "flannel", "tap", "tun",
	"kube", "cali", "weave",
}

// carriesHostTraffic reports whether an interface's counters belong in the
// machine's total.
func carriesHostTraffic(name string) bool {
	if name == "lo" {
		return false
	}
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// HasPressure reports whether the kernel answered the pressure files.
func (m Metrics) HasPressure() bool { return m.Pressure.Present }

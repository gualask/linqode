package compose

// Container resources read from the kernel instead of asked of the daemon.
//
// `docker stats` costs about two seconds whatever the project's size: the
// daemon reads each container's cgroups twice, a second apart, to derive a
// CPU percentage. Reading the same files ourselves costs what any other
// `/proc` read costs — a couple of milliseconds — because the second reading
// is the previous sample, which the client already has.
//
// Everything here is world-readable. The cgroup files are, and so is
// `/proc/<pid>/net/dev` of a container's process: the ptrace check that
// guards `environ` and `mem` does not apply to `net`, which is why the
// network counters need no privilege either (verified against the fixture as
// the unprivileged operator account).

import (
	"strconv"
	"strings"
	"time"
)

// CgroupReading is one container's raw counters. They are cumulative, which
// is why a single reading cannot say what a percentage is: that takes two.
type CgroupReading struct {
	// CPUMicros is CPU time consumed since the container started.
	CPUMicros uint64
	// MemBytes is what the container is using, page cache excluded — the
	// number `docker stats` reports, and the one an operator can act on.
	MemBytes uint64
	// MemLimitBytes is 0 when the container has no limit of its own; the
	// host's memory stands in for it, as docker does.
	MemLimitBytes uint64
	ReadBytes     uint64
	WriteBytes    uint64
	PIDs          uint64
	// RxBytes and TxBytes are the container's own interfaces, loopback
	// excluded: traffic a container sends to itself is not traffic. A
	// container whose counters are legitimately zero still has an entry in
	// the sample, so "read zero" and "could not read" stay distinguishable.
	RxBytes uint64
	TxBytes uint64
}

// CgroupSample is one reading of every container on the host, keyed by
// container id, plus the pid-keyed network counters that arrived with it.
type CgroupSample struct {
	// At is when the reply arrived, on the client's clock.
	At time.Time
	// Uptime is the host's /proc/uptime read in the same batch as the
	// counters, and zero where the host did not answer it.
	Uptime     time.Duration
	Containers map[string]CgroupReading
	Networks   map[int]CgroupReading
}

// interval is how long the counters accumulated between two samples. The
// host's own clock is the one they were accumulated against: the client's
// says when each reply *arrived*, so a reply delayed by a slow link and the
// one after it arriving promptly would turn a steady load into a dip and a
// spike. The client's clock stands in only where a host did not answer. A
// host clock that went backwards is a reboot, and no interval at all.
func (previous CgroupSample) interval(current CgroupSample) time.Duration {
	if previous.Uptime > 0 && current.Uptime > 0 {
		return current.Uptime - previous.Uptime
	}
	return current.At.Sub(previous.At)
}

func saturatingSub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

// Reading finds a container's counters. `compose ps` reports the short id and
// the cgroup directory is named after the full one, so matching on a prefix is
// the rule here rather than a leniency — with a floor of twelve characters,
// which is the length docker itself considers unambiguous.
func (s CgroupSample) Reading(id string) (CgroupReading, bool) {
	if reading, ok := s.Containers[id]; ok {
		return reading, true
	}
	if len(id) < shortIDLength {
		return CgroupReading{}, false
	}
	for full, reading := range s.Containers {
		if strings.HasPrefix(full, id) {
			return reading, true
		}
	}
	return CgroupReading{}, false
}

// shortIDLength is how many characters docker prints when it abbreviates a
// container id, and the shortest prefix worth matching on.
const shortIDLength = 12

// Delta turns two readings into one sample's worth of stats, in the shape
// `docker stats` would have reported. Keeping that shape is deliberate: the
// live stream still comes from docker, and one representation means the table
// cannot tell where a row came from.
//
// The percentages are the only thing a single reading cannot give. CPU is the
// time consumed between the two readings against the host's clock between
// them, so a container using two cores reads as 200% — docker's convention.
func (previous CgroupSample) Delta(current CgroupSample, services []Service, hostMemBytes uint64) []ContainerStats {
	elapsed := previous.interval(current)
	sample := make([]ContainerStats, 0, len(services))
	for _, service := range services {
		reading, ok := current.Reading(service.ID)
		if !ok {
			continue
		}
		stats := ContainerStats{Name: service.Name, ID: service.ID}

		stats.CPUPerc = "-"
		if before, seen := previous.Reading(service.ID); seen && elapsed > 0 {
			busy := saturatingSub(reading.CPUMicros, before.CPUMicros)
			percent := float64(busy) / float64(elapsed.Microseconds()) * 100
			stats.CPUPerc = formatPercent(percent)
		}

		limit := reading.MemLimitBytes
		if limit == 0 {
			// No limit of its own: the host's memory is the ceiling, which
			// is what docker shows too.
			limit = hostMemBytes
		}
		stats.MemUsage = formatBinary(reading.MemBytes) + " / " + formatBinary(limit)
		if limit > 0 {
			stats.MemPerc = formatPercent(float64(reading.MemBytes) / float64(limit) * 100)
		}

		stats.BlockIO = formatDecimal(reading.ReadBytes) + " / " + formatDecimal(reading.WriteBytes)
		// A container on the host's network shares the host's interfaces,
		// so its process's counters are the machine's: unknown, not those.
		if service.HostNetwork {
			stats.NetIO = "-"
		} else if network, ok := current.Networks[service.Pid]; ok {
			stats.NetIO = formatDecimal(network.RxBytes) + " / " + formatDecimal(network.TxBytes)
		}
		if reading.PIDs > 0 {
			stats.PIDs = strconv.FormatUint(reading.PIDs, 10)
		}
		sample = append(sample, stats)
	}
	return sample
}

func formatPercent(percent float64) string {
	return strconv.FormatFloat(percent, 'f', 2, 64) + "%"
}

// The unit ladders docker uses: binary for memory, decimal for the I/O
// totals. Four significant digits, which is what makes "12.34MiB" and
// "1.234GiB" line up in a column.
var (
	binaryUnits  = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	decimalUnits = []string{"B", "kB", "MB", "GB", "TB", "PB"}
)

func formatBinary(bytes uint64) string { return formatBytes(bytes, 1024, binaryUnits) }

func formatDecimal(bytes uint64) string { return formatBytes(bytes, 1000, decimalUnits) }

func formatBytes(bytes uint64, step float64, units []string) string {
	value, unit := float64(bytes), 0
	for value >= step && unit < len(units)-1 {
		value /= step
		unit++
	}
	if unit == 0 {
		return strconv.FormatUint(bytes, 10) + units[0]
	}
	return strconv.FormatFloat(value, 'g', 4, 64) + units[unit]
}

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

// The marked sections of a cgroup sample. The markers keep the parser
// independent of which globs matched and in what order the shell expanded
// them.
const (
	cgroupCPUMarker  = "#cgcpu"
	cgroupMemMarker  = "#cgmem"
	cgroupFileMarker = "#cgfile"
	cgroupMaxMarker  = "#cgmax"
	cgroupIOMarker   = "#cgio"
	cgroupPIDsMarker = "#cgpids"
	cgroupNetMarker  = "#cgnet"
)

// The two places a container's cgroup lives under v2 — the cgroupfs driver
// puts it under `docker/`, the systemd driver under a `.scope` — and the
// controller-per-directory layout of v1. All four are globbed in one command:
// a path that does not exist matches nothing and costs nothing.
var cgroupGlobs = map[string][]string{
	"cpu": {
		"/sys/fs/cgroup/docker/*/cpu.stat",
		"/sys/fs/cgroup/system.slice/docker-*.scope/cpu.stat",
		"/sys/fs/cgroup/cpuacct/docker/*/cpuacct.usage",
	},
	"mem": {
		"/sys/fs/cgroup/docker/*/memory.current",
		"/sys/fs/cgroup/system.slice/docker-*.scope/memory.current",
		"/sys/fs/cgroup/memory/docker/*/memory.usage_in_bytes",
	},
	"file": {
		"/sys/fs/cgroup/docker/*/memory.stat",
		"/sys/fs/cgroup/system.slice/docker-*.scope/memory.stat",
		"/sys/fs/cgroup/memory/docker/*/memory.stat",
	},
	"max": {
		"/sys/fs/cgroup/docker/*/memory.max",
		"/sys/fs/cgroup/system.slice/docker-*.scope/memory.max",
		"/sys/fs/cgroup/memory/docker/*/memory.limit_in_bytes",
	},
	"io": {
		"/sys/fs/cgroup/docker/*/io.stat",
		"/sys/fs/cgroup/system.slice/docker-*.scope/io.stat",
		"/sys/fs/cgroup/blkio/docker/*/blkio.throttle.io_service_bytes",
	},
	"pids": {
		"/sys/fs/cgroup/docker/*/pids.current",
		"/sys/fs/cgroup/system.slice/docker-*.scope/pids.current",
		"/sys/fs/cgroup/pids/docker/*/pids.current",
	},
}

// StatsCgroupCommand builds the remote command sampling every container's
// resources in one round-trip. The globs are fixed, so the command does not
// grow with the project: it reports every container on the host and the
// caller keeps the ones it asked about.
//
// The network counters are the exception, since they are addressed by process
// rather than by container: the pids come from the same `docker inspect` that
// already answers the restart counts.
func StatsCgroupCommand(pids []int) string {
	var b strings.Builder
	section := func(marker, pattern string, globs []string) {
		b.WriteString("echo '" + marker + "'; grep -H " + shellQuote(pattern) + " ")
		b.WriteString(strings.Join(globs, " "))
		b.WriteString(" 2>/dev/null; ")
	}
	// Only the lines that carry a number: `cpu.stat` has half a dozen
	// counters and `memory.stat` has forty.
	section(cgroupCPUMarker, "^usage_usec\\|^[0-9]", cgroupGlobs["cpu"])
	section(cgroupMemMarker, "^[0-9]", cgroupGlobs["mem"])
	section(cgroupFileMarker, "^inactive_file \\|^total_inactive_file ", cgroupGlobs["file"])
	section(cgroupMaxMarker, "", cgroupGlobs["max"])
	section(cgroupIOMarker, "", cgroupGlobs["io"])
	section(cgroupPIDsMarker, "^[0-9]", cgroupGlobs["pids"])

	b.WriteString("echo '" + cgroupNetMarker + "'; ")
	if len(pids) > 0 {
		paths := make([]string, 0, len(pids))
		for _, pid := range pids {
			if pid > 0 {
				paths = append(paths, "/proc/"+strconv.Itoa(pid)+"/net/dev")
			}
		}
		if len(paths) > 0 {
			b.WriteString("grep -H ': ' " + strings.Join(paths, " ") + " 2>/dev/null; ")
		}
	}
	return strings.TrimSuffix(b.String(), "; ")
}

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
	At         time.Time
	Containers map[string]CgroupReading
	Networks   map[int]CgroupReading
}

// ParseCgroupSample reads the output of StatsCgroupCommand. Sections that did
// not match anything leave their readings zero rather than failing the
// sample: a host with cgroup v1, or without the pids controller, still yields
// everything else.
func ParseCgroupSample(raw []byte, at time.Time) CgroupSample {
	sample := CgroupSample{At: at,
		Containers: map[string]CgroupReading{}, Networks: map[int]CgroupReading{}}
	reading := func(id string) CgroupReading { return sample.Containers[id] }
	store := func(id string, r CgroupReading) { sample.Containers[id] = r }

	// In this order, not the map's: the page cache is subtracted from the
	// memory reading, so it has to arrive after it. Ranging over the
	// sections would have made the result depend on Go's map iteration
	// order — right about half the time, which is the worst kind of right.
	sections := cgroupSections(string(raw))
	order := []string{cgroupCPUMarker, cgroupMemMarker, cgroupFileMarker,
		cgroupMaxMarker, cgroupIOMarker, cgroupPIDsMarker, cgroupNetMarker}
	for _, marker := range order {
		for line := range strings.Lines(sections[marker]) {
			path, value, found := strings.Cut(line, ":")
			if !found {
				continue
			}
			value = strings.TrimSpace(value)
			if marker == cgroupNetMarker {
				parseNetLine(sample.Networks, path, value)
				continue
			}
			id := containerID(path)
			if id == "" {
				continue
			}
			r := reading(id)
			switch marker {
			case cgroupCPUMarker:
				// v2 reports microseconds in a labelled line, v1 reports
				// nanoseconds as the whole file.
				if number, ok := strings.CutPrefix(value, "usage_usec"); ok {
					r.CPUMicros = parseUint(number)
				} else if nanos := parseUint(value); nanos > 0 {
					r.CPUMicros = nanos / 1000
				}
			case cgroupMemMarker:
				r.MemBytes = parseUint(value)
			case cgroupFileMarker:
				// Page cache the kernel can reclaim is not what the
				// container is using, and docker subtracts it too.
				fields := strings.Fields(value)
				if len(fields) == 2 {
					r.MemBytes = saturatingSub(r.MemBytes, parseUint(fields[1]))
				}
			case cgroupMaxMarker:
				// "max" means unlimited under v2; v1 writes a number so
				// large it means the same thing.
				if value != "max" {
					if limit := parseUint(value); limit < 1<<62 {
						r.MemLimitBytes = limit
					}
				}
			case cgroupIOMarker:
				read, write := parseIOLine(value)
				r.ReadBytes += read
				r.WriteBytes += write
			case cgroupPIDsMarker:
				r.PIDs = parseUint(value)
			}
			store(id, r)
		}
	}
	return sample
}

// cgroupSections splits the output on its markers.
func cgroupSections(raw string) map[string]string {
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
		if marker := strings.TrimSpace(line); strings.HasPrefix(marker, "#cg") {
			flush()
			current = marker
			continue
		}
		body.WriteString(line)
	}
	flush()
	return sections
}

// containerID pulls the container id out of a cgroup path, whichever of the
// four layouts it came from.
func containerID(path string) string {
	segments := strings.Split(path, "/")
	for index := len(segments) - 1; index >= 0; index-- {
		segment := segments[index]
		if id, ok := strings.CutPrefix(segment, "docker-"); ok {
			if id, ok := strings.CutSuffix(id, ".scope"); ok {
				return id
			}
		}
		if isHexID(segment) {
			return segment
		}
	}
	return ""
}

// isHexID reports whether a path segment looks like a container id rather
// than a controller directory.
func isHexID(segment string) bool {
	if len(segment) < 12 {
		return false
	}
	for _, r := range segment {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// parseNetLine accumulates one interface's counters onto its process. The
// loopback is skipped: a container talking to itself is not network traffic,
// and docker leaves it out too.
func parseNetLine(networks map[int]CgroupReading, path, value string) {
	pid := pidOf(path)
	if pid == 0 {
		return
	}
	name, counters, found := strings.Cut(value, ":")
	if !found {
		return
	}
	if strings.TrimSpace(name) == "lo" {
		// Still record the process: a container with only loopback has read
		// zero bytes, which is a reading, not a missing one.
		if _, seen := networks[pid]; !seen {
			networks[pid] = CgroupReading{}
		}
		return
	}
	fields := strings.Fields(counters)
	if len(fields) < 9 {
		return
	}
	reading := networks[pid]
	reading.RxBytes += parseUint(fields[0])
	reading.TxBytes += parseUint(fields[8])
	networks[pid] = reading
}

// pidOf reads the process id out of a `/proc/<pid>/net/dev` path.
func pidOf(path string) int {
	rest, ok := strings.CutPrefix(path, "/proc/")
	if !ok {
		return 0
	}
	digits, _, _ := strings.Cut(rest, "/")
	pid, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return pid
}

// parseIOLine sums one line of `io.stat` (v2, `8:0 rbytes=1 wbytes=2 …`) or of
// `blkio.throttle.io_service_bytes` (v1, `8:0 Read 12345` — one line per
// operation, of which Read and Write are the two that are not sums of others).
func parseIOLine(value string) (read, write uint64) {
	fields := strings.Fields(value)
	if len(fields) >= 2 {
		switch fields[len(fields)-2] {
		case "Read":
			return parseUint(fields[len(fields)-1]), 0
		case "Write":
			return 0, parseUint(fields[len(fields)-1])
		}
	}
	for _, field := range fields {
		key, number, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case "rbytes":
			read += parseUint(number)
		case "wbytes":
			write += parseUint(number)
		}
	}
	return read, write
}

func parseUint(text string) uint64 {
	value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0
	}
	return value
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
// time consumed between the two readings against the wall clock between them,
// so a container using two cores reads as 200% — docker's convention.
func (previous CgroupSample) Delta(current CgroupSample, services []Service, hostMemBytes uint64) []ContainerStats {
	elapsed := current.At.Sub(previous.At)
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
		if network, ok := current.Networks[service.Pid]; ok {
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

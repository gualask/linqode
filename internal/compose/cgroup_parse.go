package compose

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// cgroupOrder is the sections in the order the command prints them.
var cgroupOrder = []string{cgroupCPUMarker, cgroupMemMarker, cgroupFileMarker,
	cgroupMaxMarker, cgroupIOMarker, cgroupPIDsMarker, cgroupNetMarker}

// ParseCgroupSample reads the output of StatsCgroupCommand. Sections that did
// not match anything leave their readings zero rather than failing the
// sample: a host with cgroup v1, or without the pids controller, still yields
// everything else.
func ParseCgroupSample(raw []byte, at time.Time) CgroupSample {
	p := cgroupParse{
		sample: CgroupSample{At: at,
			Containers: map[string]CgroupReading{}, Networks: map[int]CgroupReading{}},
		inactive:      map[string]uint64{},
		totalInactive: map[string]uint64{},
	}
	sections := cgroupSections(string(raw))
	if fields := strings.Fields(sections[cgroupUptimeMarker]); len(fields) > 0 {
		if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
			p.sample.Uptime = time.Duration(seconds * float64(time.Second))
		}
	}
	for _, marker := range cgroupOrder {
		for line := range strings.Lines(sections[marker]) {
			p.line(marker, line)
		}
	}
	p.subtractPageCache()
	return p.sample
}

// cgroupParse is one sample being read. The page cache is collected on the
// way and taken off the memory readings once every section is in, because
// which of its two figures applies depends on whether the host reported both.
type cgroupParse struct {
	sample                  CgroupSample
	inactive, totalInactive map[string]uint64
}

// line applies one `path:value` line of a section to the container or the
// process its path names.
func (p *cgroupParse) line(marker, line string) {
	path, value, found := strings.Cut(line, ":")
	if !found {
		return
	}
	value = strings.TrimSpace(value)
	if marker == cgroupNetMarker {
		parseNetLine(p.sample.Networks, path, value)
		return
	}
	id := containerID(path)
	if id == "" {
		return
	}
	reading := p.sample.Containers[id]
	p.apply(marker, id, value, &reading)
	p.sample.Containers[id] = reading
}

// apply reads one container's value for the section it came from.
func (p *cgroupParse) apply(marker, id, value string, reading *CgroupReading) {
	switch marker {
	case cgroupCPUMarker:
		if micros, ok := cpuMicros(value); ok {
			reading.CPUMicros = micros
		}
	case cgroupMemMarker:
		reading.MemBytes = parseUint(value)
	case cgroupFileMarker:
		p.pageCache(id, value)
	case cgroupMaxMarker:
		if limit, ok := memLimit(value); ok {
			reading.MemLimitBytes = limit
		}
	case cgroupIOMarker:
		read, write := parseIOLine(value)
		reading.ReadBytes += read
		reading.WriteBytes += write
	case cgroupPIDsMarker:
		reading.PIDs = parseUint(value)
	}
}

// cpuMicros reads CPU time: v2 reports microseconds in a labelled line, v1
// nanoseconds as the whole file. v2's other lines (user_usec, system_usec)
// are not it, and leave the reading alone.
func cpuMicros(value string) (uint64, bool) {
	if number, ok := strings.CutPrefix(value, "usage_usec"); ok {
		return parseUint(number), true
	}
	if nanos := parseUint(value); nanos > 0 {
		return nanos / 1000, true
	}
	return 0, false
}

// memLimit reads a memory limit. "max" means unlimited under v2; v1 writes a
// number so large it means the same thing, and neither is a limit.
func memLimit(value string) (uint64, bool) {
	if value == "max" {
		return 0, false
	}
	limit := parseUint(value)
	return limit, limit < 1<<62
}

// pageCache records one line of memory.stat worth keeping: the inactive file
// cache, under whichever of its two names the host uses.
func (p *cgroupParse) pageCache(id, value string) {
	fields := strings.Fields(value)
	if len(fields) != 2 {
		return
	}
	switch fields[0] {
	case "inactive_file":
		p.inactive[id] = parseUint(fields[1])
	case "total_inactive_file":
		p.totalInactive[id] = parseUint(fields[1])
	}
}

// subtractPageCache takes the page cache off every memory reading. Docker
// uses the hierarchical total under v1, which reports both fields, and
// inactive_file under v2. Never subtract both.
func (p *cgroupParse) subtractPageCache() {
	for id, reading := range p.sample.Containers {
		inactive := p.inactive[id]
		if total, ok := p.totalInactive[id]; ok {
			inactive = total
		}
		reading.MemBytes = saturatingSub(reading.MemBytes, inactive)
		p.sample.Containers[id] = reading
	}
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

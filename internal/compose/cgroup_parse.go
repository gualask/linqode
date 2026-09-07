package compose

import (
	"strconv"
	"strings"
	"time"
)

// ParseCgroupSample reads the output of StatsCgroupCommand. Sections that did
// not match anything leave their readings zero rather than failing the
// sample: a host with cgroup v1, or without the pids controller, still yields
// everything else.
func ParseCgroupSample(raw []byte, at time.Time) CgroupSample {
	sample := CgroupSample{At: at,
		Containers: map[string]CgroupReading{}, Networks: map[int]CgroupReading{}}
	reading := func(id string) CgroupReading { return sample.Containers[id] }
	store := func(id string, r CgroupReading) { sample.Containers[id] = r }
	cache := map[string]uint64{}
	totalCache := map[string]uint64{}

	// Read sections in command order; cache is applied once after parsing.
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
				fields := strings.Fields(value)
				if len(fields) == 2 {
					switch fields[0] {
					case "inactive_file":
						cache[id] = parseUint(fields[1])
					case "total_inactive_file":
						totalCache[id] = parseUint(fields[1])
					}
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
	for id, r := range sample.Containers {
		// Docker uses the hierarchical total under v1, which reports both
		// fields, and inactive_file under v2. Never subtract both.
		inactive := cache[id]
		if total, ok := totalCache[id]; ok {
			inactive = total
		}
		r.MemBytes = saturatingSub(r.MemBytes, inactive)
		store(id, r)
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

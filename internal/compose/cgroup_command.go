package compose

import (
	"strconv"
	"strings"
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
	// cgroupUptimeMarker is the host's clock, which the CPU counters are
	// divided by rather than the client's.
	cgroupUptimeMarker = "#cguptime"
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
//
// /proc/uptime goes first, read in the same round trip: it is the interval
// the CPU counters accumulated over, which the reply's arrival is not.
func StatsCgroupCommand(pids []int) string {
	var b strings.Builder
	b.WriteString("echo '" + cgroupUptimeMarker + "'; cat /proc/uptime 2>/dev/null; ")
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

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

// Where a container's cgroup directory lives under v2: under `docker/` with
// the cgroupfs driver, in a `.scope` under `system.slice` with the systemd
// driver, and — rootless docker, which is always the systemd driver — in a
// `.scope` under the user's own manager, `user@<uid>.service/user.slice`.
var cgroupV2Dirs = []string{
	"/sys/fs/cgroup/docker/*",
	"/sys/fs/cgroup/system.slice/docker-*.scope",
	"/sys/fs/cgroup/user.slice/user-*.slice/user@*.service/user.slice/docker-*.scope",
}

// cgroupV1Dirs is the same for v1, where every controller is a hierarchy of
// its own: one directory per controller, for both drivers. `cpuacct` is a
// symlink to the combined `cpu,cpuacct` where the two are co-mounted, so the
// one name reaches either. Rootless docker needs v2 and has no v1 form.
func cgroupV1Dirs(controller string) []string {
	return []string{
		"/sys/fs/cgroup/" + controller + "/docker/*",
		"/sys/fs/cgroup/" + controller + "/system.slice/docker-*.scope",
	}
}

// cgroupGlobs is every layout above, per reading. All of them are globbed in
// one command: a path that does not exist matches nothing and costs nothing.
var cgroupGlobs = map[string][]string{
	"cpu":  cgroupFiles("cpu.stat", "cpuacct", "cpuacct.usage"),
	"mem":  cgroupFiles("memory.current", "memory", "memory.usage_in_bytes"),
	"file": cgroupFiles("memory.stat", "memory", "memory.stat"),
	"max":  cgroupFiles("memory.max", "memory", "memory.limit_in_bytes"),
	"io":   cgroupFiles("io.stat", "blkio", "blkio.throttle.io_service_bytes"),
	"pids": cgroupFiles("pids.current", "pids", "pids.current"),
}

// cgroupFiles names one reading's file in every v2 layout and, under the v1
// controller that holds it, in every v1 one.
func cgroupFiles(v2File, v1Controller, v1File string) []string {
	var globs []string
	for _, dir := range cgroupV2Dirs {
		globs = append(globs, dir+"/"+v2File)
	}
	for _, dir := range cgroupV1Dirs(v1Controller) {
		globs = append(globs, dir+"/"+v1File)
	}
	return globs
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

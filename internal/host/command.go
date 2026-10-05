package host

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

// markers is every section marker this package's commands print, and the
// only lines split takes for one.
var markers = map[string]bool{
	loadMarker: true, uptimeMarker: true, memMarker: true, cpuMarker: true,
	statMarker: true, netMarker: true, pressureMarker: true, diskMarker: true,
	mountsMarker: true, thermalMarker: true,
	processMarker: true, pageSizeMarker: true, clockMarker: true,
	amdMarker: true, nvidiaMarker: true, appleMarker: true,
}

// mountsCommand lists every mounted filesystem. It is separate from the
// root one, and guarded, for a reason that has nothing to do with cost: a
// df with no argument calls statfs on every mount, and a hung network mount
// makes it block for as long as the kernel's timeout allows. The root
// filesystem, the one reading that is always wanted, is read by its own df
// above and never depends on this one finishing.
var mountsCommand = bounded("df -Pk 2>/dev/null")

// bounded runs command under a five-second `timeout` where the host has one
// that works, and unbounded where it does not — which is what every host did
// before the bound existed, so a missing guard never loses the reading.
//
// "Works" is asked by running it, not by `command -v`: BusyBox before 1.30
// has a `timeout` that wants `-t 5`, reads `timeout 5 df` as a program named
// `5`, and prints an error where the reading should be. `timeout 1 true`
// fails the same way there and succeeds everywhere else, for the price of
// two process starts.
func bounded(command string) string {
	return "if " + timeoutWorks + "; then timeout 5 " + command + "; else " + command + "; fi"
}

// timeoutWorks is the guard bounded uses. internal/probe keeps a copy for
// its daemon question; the two packages share nothing else.
const timeoutWorks = "timeout 1 true >/dev/null 2>&1"

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

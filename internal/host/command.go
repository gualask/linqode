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

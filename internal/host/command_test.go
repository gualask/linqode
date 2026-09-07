package host

import (
	"strings"
	"testing"
)

func TestCommandAsksForEverySection(t *testing.T) {
	command := Command()
	for _, marker := range []string{loadMarker, uptimeMarker, memMarker, cpuMarker,
		statMarker, netMarker, pressureMarker, diskMarker, mountsMarker} {
		if !strings.Contains(command, marker) {
			t.Errorf("Command() does not emit marker %q", marker)
		}
	}
	// -k pins df to 1024-byte blocks; without it the block size varies and
	// every disk number would be wrong by a factor of two.
	if !strings.Contains(command, "df -Pk /") {
		t.Error("Command() must use `df -Pk /` for portable, 1024-byte-block output")
	}
	// A pressure file is absent on kernels without PSI, and its absence is
	// not an error worth putting on the operator's screen.
	if !strings.Contains(command, "2>/dev/null") {
		t.Error("a missing /proc/pressure would write to stderr")
	}
	// The mount list can block on a hung network mount. The root reading
	// must not be behind it, and a host without `timeout` must still get it.
	if !strings.Contains(command, "timeout 5 df -Pk") ||
		!strings.Contains(command, "else df -Pk") {
		t.Error("the mount list is neither guarded nor able to do without the guard")
	}
}

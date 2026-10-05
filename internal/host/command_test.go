package host

import (
	"os"
	"os/exec"
	"path/filepath"
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

// BusyBox before 1.30 has a `timeout` that wants `-t 5`: `command -v` finds
// it, and `timeout 5 df` then runs a program named `5`. Run through a real
// shell against that `timeout`, the mount list must still be read, unbounded;
// against one that works, bounded.
func TestAnOldBusyBoxTimeoutDoesNotLoseTheMountList(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	for name, timeout := range map[string]string{
		"pre-1.30 busybox": "[ \"$1\" = -t ] || { echo \"timeout: can't execute '$1'\" >&2; exit 127; }\nshift 2; exec \"$@\"\n",
		"working":          "[ \"$2\" = true ] || : > \"$0.used\"; shift; exec \"$@\"\n",
	} {
		bin := t.TempDir()
		writeScript(t, bin, "timeout", timeout)
		writeScript(t, bin, "true", "exit 0\n")
		writeScript(t, bin, "df", "echo '/dev/sda1 100 50 50 50% /'\n")
		cmd := exec.Command(sh, "-c", mountsCommand)
		cmd.Env = []string{"PATH=" + bin}
		out, err := cmd.Output()
		if err != nil || !strings.Contains(string(out), "/dev/sda1") {
			t.Errorf("%s: mount list = %q, %v", name, out, err)
		}
		_, statErr := os.Stat(filepath.Join(bin, "timeout.used"))
		if bounded := statErr == nil; bounded != (name == "working") {
			t.Errorf("%s: bounded = %v", name, bounded)
		}
	}
}

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

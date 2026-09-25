package local

import (
	"os/exec"
	"syscall"
)

// The local target is not offered on Windows (see cmd/linqode), and this file
// exists so the binary builds there for everything else: Windows has neither
// process groups to signal nor the `sh` every command here runs under.

// ownGroup does nothing: there is no process group to start the command in.
func ownGroup(*exec.Cmd) {}

// signalGroup kills the one process, whatever the signal: Windows cannot
// deliver SIGTERM, and there is no group to reach its children through.
func signalGroup(cmd *exec.Cmd, _ syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

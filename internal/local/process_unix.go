//go:build !windows

package local

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the command in a process group of its own.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup sends sig to the command's process group. The negative pid is
// what makes it the group rather than the one process, and the group's id is
// the started process's pid because it is the one that created it.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

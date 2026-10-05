package local

// Starting a command here is three lines; ending one is the rest of this
// file, and is the whole reason the package exists.
//
// `sh -c 'docker compose logs -f'` is a single command, so the shell execs
// and *becomes* it: killing the process we started kills the right thing, by
// accident. Add a pipe — which any configured script may have — and the
// shell stays alive as the parent of its children. Signal only the process
// we started and the children survive, reparented to init, writing into a
// pipe nobody reads until the kernel buffer fills and they block there for
// good. Every opened and closed follow view would leave one behind.
//
// So the command gets a process group of its own and the signal goes to the
// group. That is the negative pid in Kill, and it is the only interesting
// line here. It lives in process_unix.go: process groups are a Unix idea, and
// Windows, where the local target is not offered, gets the one process killed
// instead (process_windows.go), so that the binary still builds there.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	// killGrace is how long a cancelled command has to honour SIGTERM
	// before it is killed. `docker compose logs -f` closes in
	// milliseconds; this is for whoever ignores the signal.
	killGrace = time.Second
	// pipeGrace bounds the wait after the command itself has exited but
	// something it started still holds the output pipe — a script ending
	// in `daemon &`. Without it that wait never ends.
	pipeGrace = time.Second
	// closeGrace bounds Close: the kill grace and then the pipe grace are
	// the longest a cancelled command takes to be reaped, and the rest is
	// margin for a loaded machine.
	closeGrace = killGrace + pipeGrace + time.Second
)

// command builds one command. Everything runs under `sh -c`, matching the
// remote rule, which is read once and then known, where "it depends on your
// login shell" is behaviour discovered after a debugging session.
func (s *Session) command(command string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = s.dir
	// The operator's environment is inherited — without their PATH docker
	// would not be found — but the readings are parsed, and a parser cannot
	// afford a locale: `sysctl -n vm.loadavg` prints `{ 1,41 1,31 1,26 }`
	// on an Italian machine. sshd's minimal environment gives the remote
	// path this for free. The Executor cannot tell a reading from a script
	// (both arrive as a string), so this holds for everything it runs; the
	// alternative is a rule nobody can state.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	// A group of its own, so that terminate can reach the children too.
	ownGroup(cmd)
	cmd.WaitDelay = pipeGrace
	return cmd
}

// start refuses to spawn anything for a context that is already done. It is
// the local analogue of the remote path's check before opening a channel;
// there is no handshake to bound here, because starting a process does not
// wait on a peer.
func start(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return cmd.Start()
}

// wait joins the command, tearing it down if ctx is cancelled first. The
// process is always reaped before returning: an ExitError left unread is a
// zombie held for the life of the session.
func wait(ctx context.Context, cmd *exec.Cmd) error {
	finished := make(chan struct{})
	var err error
	go func() {
		err = cmd.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-ctx.Done():
		terminate(cmd, finished)
		<-finished
		return err
	}
	// The command exited and its output pipe outlived it, which is what
	// pipeGrace just waited out. What is holding it is still running and is
	// no longer anybody's child, so it goes the same way a cancelled
	// command does.
	if errors.Is(err, exec.ErrWaitDelay) {
		signalGroup(cmd, syscall.SIGKILL)
	}
	return err
}

// terminate ends a cancelled command and everything it started: SIGTERM to
// the whole process group first, so a follower can close cleanly, then
// SIGKILL for whoever is left. The kill is bounded by the command actually
// finishing, so a recycled pid is never signalled.
func terminate(cmd *exec.Cmd, finished <-chan struct{}) {
	signalGroup(cmd, syscall.SIGTERM)
	timer := time.NewTimer(killGrace)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
		signalGroup(cmd, syscall.SIGKILL)
	}
}

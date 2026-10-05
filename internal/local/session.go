// Package local runs commands on the machine Linqode itself runs on.
//
// It is the second implementation of the two methods internal/operations
// depends on, and nothing above that line can tell which one it holds: the
// same sampler, probe, parsers and actions run against it unchanged. That is
// the point of the seam — a local target is a second Executor, not a second
// application.
//
// The types it returns are remote's on purpose. ExecOutput and ExecEvent
// describe the outcome of running a command, not the transport that carried
// it, and a second pair of identical types would force every caller above to
// learn which one it is looking at.
//
// Windows is out of scope here as it is everywhere else in this codebase:
// every command the tool builds is shell, quoted for POSIX.
package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gualask/linqode/internal/remote"
)

// Session is the local machine wearing the shape of a connection, so that
// the composition root can hold either one. There is nothing to establish
// and nothing to authenticate; what state there is says where commands run,
// and which of them are still running.
type Session struct {
	// dir is the operator's home, matching the remote rule that scripts and
	// `!` run in the login directory, like `ssh host 'command'`. Compose
	// commands are unaffected: they carry their own directory.
	dir string

	// scope ends with the session, and every command runs under it as well
	// as under its caller's context. Closing an SSH connection takes every
	// channel on it down at the far end; here nothing does that for free —
	// each command is a process group of its own, out of reach of the
	// terminal's SIGINT and SIGHUP — so Close has to do it, and running is
	// how it knows when it has.
	scope   context.Context
	end     context.CancelFunc
	mu      sync.Mutex // orders closed against running.Add
	closed  bool
	running sync.WaitGroup
}

// errClosed is what a command asked of a closed session gets.
var errClosed = errors.New("the local session is closed")

// New opens a session on this machine. A machine with no home directory —
// $HOME unset — is not one Linqode is running on in any ordinary sense, and
// commands then inherit the working directory Linqode was started in, which
// is the closest honest thing to do.
func New() *Session {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	scope, end := context.WithCancel(context.Background())
	return &Session{dir: home, scope: scope, end: end}
}

// Close ends every command the session started, whether or not its caller
// ever cancelled it, and waits for them to be reaped — bounded, because a
// teardown that could hang is worse than one that gives up. The bound covers
// the slowest path a command can take to end: the kill grace, then the wait
// for its pipes. Closing twice is harmless.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.end()

	reaped := make(chan struct{})
	go func() {
		s.running.Wait()
		close(reaped)
	}()
	timer := time.NewTimer(closeGrace)
	defer timer.Stop()
	select {
	case <-reaped:
	case <-timer.C:
	}
}

// enter binds one command to the session: the context it returns ends with
// whichever of ctx and the session ends first, and the command counts as
// running until leave is called, once it has been reaped.
func (s *Session) enter(ctx context.Context) (bound context.Context, leave func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, errClosed
	}
	bound, cancel := context.WithCancel(ctx)
	unbind := context.AfterFunc(s.scope, cancel)
	s.running.Add(1)
	return bound, func() {
		unbind()
		cancel()
		s.running.Done()
	}, nil
}

// Exec runs command and collects its output until it finishes or ctx is
// cancelled.
func (s *Session) Exec(ctx context.Context, command string) (remote.ExecOutput, error) {
	ctx, leave, err := s.enter(ctx)
	if err != nil {
		return remote.ExecOutput{}, err
	}
	defer leave()

	cmd := s.command(command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := start(ctx, cmd); err != nil {
		return remote.ExecOutput{}, err
	}
	err = wait(ctx, cmd)
	if ctx.Err() != nil {
		return remote.ExecOutput{}, ctx.Err()
	}

	out := remote.ExecOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: -1}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		out.ExitCode = 0
	case errors.Is(err, exec.ErrWaitDelay):
		// The command succeeded and something it left behind is still
		// holding the output pipe. The output is whole — the command wrote
		// it before exiting — so this is an exit code of zero, and the
		// holder has already been dealt with by wait.
		out.ExitCode = 0
	case errors.As(err, &exitErr):
		// ExitCode is -1 for a command killed by a signal, which is the
		// same -1 the remote path reports when sshd sends no exit status.
		out.ExitCode = exitErr.ExitCode()
	default:
		return out, err
	}
	return out, nil
}

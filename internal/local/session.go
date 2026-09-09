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

	"github.com/gualask/linqode/internal/remote"
)

// Session is the local machine wearing the shape of a connection, so that
// the composition root can hold either one. There is nothing to establish
// and nothing to authenticate; what little state there is says where
// commands run.
type Session struct {
	// dir is the operator's home, matching the remote rule that scripts and
	// `!` run in the login directory, like `ssh host 'command'`. Compose
	// commands are unaffected: they carry their own directory.
	dir string
}

// New opens a session on this machine. A machine with no home directory —
// $HOME unset — is not one Linqode is running on in any ordinary sense, and
// commands then inherit the working directory Linqode was started in, which
// is the closest honest thing to do.
func New() *Session {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return &Session{dir: home}
}

// Close releases nothing. It exists because the caller's other option has
// one, and a local session that had to be special-cased at teardown would be
// a seam that leaks.
func (s *Session) Close() {}

// Exec runs command and collects its output until it finishes or ctx is
// cancelled.
func (s *Session) Exec(ctx context.Context, command string) (remote.ExecOutput, error) {
	cmd := s.command(command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := start(ctx, cmd); err != nil {
		return remote.ExecOutput{}, err
	}
	err := wait(ctx, cmd)
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

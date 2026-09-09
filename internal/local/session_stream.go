package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"

	"github.com/gualask/linqode/internal/remote"
)

// ExecStream starts command and streams its output as events. The channel
// closes when the output ends; ExecExit is the last event when the command
// reported a status. Cancelling ctx terminates the command and everything it
// started (see terminate) and closes the channel, so an abandoned stream
// never leaks a follower process.
//
// Like the remote path, the stream ends with the *output*, not with the
// process: a command that exits leaving a background child holding the pipe
// keeps the stream open, exactly as `ssh host 'daemon &'` hangs. The caller's
// context is what ends it.
func (s *Session) ExecStream(ctx context.Context, command string) (<-chan remote.ExecEvent, error) {
	cmd := s.command(command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return nil, err
	}
	if err := start(ctx, cmd); err != nil {
		return nil, err
	}

	events := make(chan remote.ExecEvent, 32)
	var readers sync.WaitGroup
	readers.Add(2)
	go pump(ctx, stdout, remote.ExecStdout, events, &readers)
	go pump(ctx, stderr, remote.ExecStderr, events, &readers)

	go finish(ctx, cmd, events, &readers)
	return events, nil
}

func pump(ctx context.Context, r io.Reader, kind remote.ExecEventKind, events chan<- remote.ExecEvent, readers *sync.WaitGroup) {
	defer readers.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 && !sendEvent(ctx, events, remote.ExecEvent{Kind: kind, Data: bytes.Clone(buf[:n])}) {
			return
		}
		if err != nil {
			return
		}
	}
}

func sendEvent(ctx context.Context, events chan<- remote.ExecEvent, event remote.ExecEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// finish tears the command down on cancel, which unblocks the readers; with
// no cancel it waits for them and reports the exit. The readers are joined
// before Wait rather than after it, because os/exec closes the pipes there
// and a read still in flight would lose the last of the output.
func finish(ctx context.Context, cmd *exec.Cmd, events chan remote.ExecEvent, readers *sync.WaitGroup) {
	defer close(events)
	finished := make(chan struct{})
	var err error
	go func() {
		readers.Wait()
		err = cmd.Wait()
		close(finished)
	}()

	select {
	case <-ctx.Done():
		terminate(cmd, finished)
		<-finished
		return
	case <-finished:
	}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		sendEvent(ctx, events, remote.ExecEvent{Kind: remote.ExecExit, ExitCode: 0})
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code >= 0 {
			sendEvent(ctx, events, remote.ExecEvent{Kind: remote.ExecExit, ExitCode: code})
		}
		// Killed by a signal: no exit to report, as when sshd sends none.
	default:
		// Cancelled, or the process could not be reaped.
	}
}

package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

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

	go finish(ctx, cmd, events, &readers, stdout, stderr)
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

// release closes the read ends of a terminated command's pipes if its readers
// have not finished within pipeGrace, which unblocks them: the group is dead
// by now, and whatever still holds the write ends is not anything terminate
// could reach. Closing an *os.File under a blocked Read is what the runtime
// poller is for, and Wait closing them again afterwards is harmless.
func release(finished <-chan struct{}, pipes []io.Closer) {
	timer := time.NewTimer(pipeGrace)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
		for _, pipe := range pipes {
			_ = pipe.Close()
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
//
// That order is also why cmd.WaitDelay does nothing here: it bounds Wait,
// and Wait is not reached while a reader is blocked. A descendant that left
// the process group — `setsid daemon &` — is out of terminate's reach and
// keeps the pipes open, so on cancel the parent's read ends are closed once
// the grace runs out: what cannot be killed is stopped being listened to.
func finish(ctx context.Context, cmd *exec.Cmd, events chan remote.ExecEvent, readers *sync.WaitGroup,
	pipes ...io.Closer) {
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
		release(finished, pipes)
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

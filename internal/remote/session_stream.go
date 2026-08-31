package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// ExecEventKind discriminates the events of a streaming command.
type ExecEventKind int

const (
	// ExecStdout carries a chunk of standard output in Data.
	ExecStdout ExecEventKind = iota
	// ExecStderr carries a chunk of standard error in Data.
	ExecStderr
	// ExecExit carries the command's exit code in ExitCode; it is the last
	// event when the remote side reports one.
	ExecExit
)

// ExecEvent is one chunk of output, or the exit report, of a streaming
// remote command.
type ExecEvent struct {
	Kind     ExecEventKind
	Data     []byte
	ExitCode int
}

// ExecStream starts command and streams its output as events. The channel
// closes when the command ends; ExecExit is the last event when the remote
// side reports an exit code. Cancelling ctx terminates the remote command
// (SIGTERM, honored by modern sshd, then channel close — a follower that
// misses the signal dies of SIGPIPE on its next write) and closes the
// channel, so an abandoned stream never leaks a follower process.
func (s *Session) ExecStream(ctx context.Context, command string) (<-chan ExecEvent, error) {
	sess, stdout, stderr, err := s.startStreamingCommand(command)
	if err != nil {
		return nil, err
	}

	events := make(chan ExecEvent, 32)
	var readers sync.WaitGroup
	readers.Add(2)
	go pumpExecEvents(ctx, stdout, ExecStdout, events, &readers)
	go pumpExecEvents(ctx, stderr, ExecStderr, events, &readers)

	// The controller tears the command down on cancel, which unblocks the
	// readers; with no cancel it waits for them and reports the exit.
	go finishExecStream(ctx, sess, events, &readers)
	return events, nil
}

func (s *Session) startStreamingCommand(command string) (*ssh.Session, io.Reader, io.Reader, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, nil, nil, err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		sess.Close()
		return nil, nil, nil, err
	}
	if err := sess.Start(command); err != nil {
		sess.Close()
		return nil, nil, nil, err
	}
	return sess, stdout, stderr, nil
}

func pumpExecEvents(ctx context.Context, r io.Reader, kind ExecEventKind, events chan<- ExecEvent, readers *sync.WaitGroup) {
	defer readers.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 && !sendExecEvent(ctx, events, ExecEvent{Kind: kind, Data: bytes.Clone(buf[:n])}) {
			return
		}
		if err != nil {
			return
		}
	}
}

func sendExecEvent(ctx context.Context, events chan<- ExecEvent, event ExecEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func finishExecStream(ctx context.Context, sess *ssh.Session, events chan ExecEvent, readers *sync.WaitGroup) {
	defer close(events)
	finished := waitForReaders(readers)
	select {
	case <-ctx.Done():
		terminate(sess)
		<-finished
		return
	case <-finished:
	}

	err := sess.Wait()
	sess.Close()
	var exitErr *ssh.ExitError
	switch {
	case err == nil:
		sendExecEvent(ctx, events, ExecEvent{Kind: ExecExit, ExitCode: 0})
	case errors.As(err, &exitErr):
		sendExecEvent(ctx, events, ExecEvent{Kind: ExecExit, ExitCode: exitErr.ExitStatus()})
	default:
		// Cancelled or transport gone: no exit to report.
	}
}

func waitForReaders(readers *sync.WaitGroup) <-chan struct{} {
	finished := make(chan struct{})
	go func() {
		readers.Wait()
		close(finished)
	}()
	return finished
}

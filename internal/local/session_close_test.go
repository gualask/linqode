//go:build !windows

package local_test

// Close is the session's end, and nothing a session started outlives it:
// a caller that forgot to cancel a stream, or never could, must not leave a
// process group running in the background of the operator's terminal.

import (
	"context"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/local"
)

func TestCloseEndsEveryCommandTheSessionStarted(t *testing.T) {
	session := local.New()

	streamCommand, streamChild := backgroundChild(t, "sleep 30")
	events, err := session.ExecStream(context.Background(), "echo ready; "+streamCommand+"; wait")
	if err != nil {
		t.Fatal(err)
	}
	execCommand, execChild := backgroundChild(t, "sleep 30")
	execDone := make(chan error, 1)
	go func() {
		_, err := session.Exec(context.Background(), execCommand+"; wait")
		execDone <- err
	}()
	streamPID, execPID := streamChild(), execChild() // both are running now

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		session.Close()
	}()
	awaitReturn(t, closed)

	// Close has returned, so everything it waited for is already over: the
	// channel is closed rather than closing, and Exec has returned.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range events {
		}
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Error("the stream's channel was still open after Close returned")
	}
	select {
	case err := <-execDone:
		if err == nil {
			t.Error("Exec reported success for a command Close ended")
		}
	case <-time.After(time.Second):
		t.Error("Exec was still running after Close returned")
	}
	for _, pid := range []int{streamPID, execPID} {
		if !waitGone(pid, 2*time.Second) {
			t.Errorf("child %d outlived the session", pid)
		}
	}
}

// A closed session starts nothing, and closing it twice is harmless — the
// composition root defers Close, and a second caller must not hang on it.
func TestAClosedSessionStartsNothing(t *testing.T) {
	session := local.New()
	session.Close()
	session.Close()

	marker := t.TempDir() + "/ran"
	if _, err := session.Exec(testContext(t), "touch "+marker); err == nil {
		t.Error("Exec ran on a closed session")
	}
	if events, err := session.ExecStream(testContext(t), "touch "+marker); err == nil || events != nil {
		t.Errorf("ExecStream on a closed session = %v, %v", events, err)
	}
}

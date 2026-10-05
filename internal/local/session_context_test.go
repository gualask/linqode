//go:build !windows

package local_test

// Cancellation, which is the whole reason this package is not three lines.
// It mirrors internal/remote's TestCommandCancellationDuringSSHWaits: the
// same two modes, and the stages where a local command can be waiting.

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/local"
	"github.com/gualask/linqode/internal/remote"
)

// run starts one operation and returns a channel closed when it has
// returned, plus the error it returned for exec.
func run(t *testing.T, ctx context.Context, mode, command string) (<-chan struct{}, *error) {
	t.Helper()
	session := local.New()
	done := make(chan struct{})
	err := new(error)
	go func() {
		defer close(done)
		if mode == "exec" {
			_, *err = session.Exec(ctx, command)
			return
		}
		events, startErr := session.ExecStream(ctx, command)
		if startErr != nil {
			*err = startErr
			return
		}
		for range events {
		}
	}()
	return done, err
}

func awaitReturn(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(guardTimeout):
		t.Fatal("the operation never returned after the context was cancelled")
	}
}

// Nothing is spawned for a context that is already done. On the remote path
// this is the check before opening a channel.
func TestAnAlreadyCancelledContextStartsNothing(t *testing.T) {
	session := local.New()
	marker := t.TempDir() + "/ran"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := session.Exec(ctx, "touch "+marker); !errors.Is(err, context.Canceled) {
		t.Errorf("Exec returned %v, want context.Canceled", err)
	}
	events, err := session.ExecStream(ctx, "touch "+marker)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ExecStream returned %v, want context.Canceled", err)
	}
	if events != nil {
		t.Error("ExecStream returned a channel for a cancelled context")
	}
}

// The stages are the two places a local command can be waiting: with its
// output still flowing, and after the output has ended but the process has
// not. The second is where a Wait that ignores the context hangs forever.
func TestCancellationEndsTheCommandAndItsChildren(t *testing.T) {
	for _, mode := range []string{"exec", "stream"} {
		for _, stage := range []string{"while running", "after output EOF"} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				command, childPID := backgroundChild(t, "sleep 30")
				if stage == "after output EOF" {
					command += "; exec 1>&- 2>&-"
				}
				command += "; wait"

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done, err := run(t, ctx, mode, command)

				child := childPID() // it is running now, and so is its parent
				cancel()
				awaitReturn(t, done)

				if mode == "exec" && !errors.Is(*err, context.Canceled) {
					t.Errorf("Exec returned %v, want context.Canceled", *err)
				}
				if !waitGone(child, 2*time.Second) {
					t.Errorf("child %d survived the cancelled command", child)
				}
			})
		}
	}
}

// SIGTERM is a request; the group that refuses it is killed after the grace
// period rather than left running.
func TestACommandIgnoringSIGTERMIsKilled(t *testing.T) {
	// Both shells refuse the signal, and each has refused it before the
	// child reports the pid the test waits on — otherwise a cancellation
	// arriving in between would find a command that dies of SIGTERM after
	// all. `sleep` alone would not do either: it does not ignore SIGTERM,
	// so its parent's `wait` would end with it and nothing would reach
	// SIGKILL.
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	command := `trap "" TERM; sh -c 'trap "" TERM; echo $$ > ` + pidfile +
		`; while :; do sleep 0.2; done' & wait`

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, _ := run(t, ctx, "exec", command)

	child := awaitPIDFile(t, pidfile)
	start := time.Now()
	cancel()
	awaitReturn(t, done)

	if !waitGone(child, 2*time.Second) {
		t.Errorf("child %d survived SIGKILL", child)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("returned after %s, so SIGTERM was never given time to be honoured", elapsed)
	}
}

// A cancelled stream closes its channel: the view that opened it is gone,
// and a channel left open is a goroutine held for the session.
func TestACancelledStreamClosesItsChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := local.New().ExecStream(ctx, "echo ready; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Kind != remote.ExecStdout || string(event.Data) != "ready\n" {
			t.Fatalf("first event is %+v", event)
		}
	case <-time.After(guardTimeout):
		t.Fatal("no output before the cancellation")
	}

	cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for range events {
		}
	}()
	awaitReturn(t, closed)
}

// A descendant that left the process group is out of the signal's reach and
// still holds the output pipe. The stream must close anyway: what the
// cancellation cannot kill, it stops listening to.
func TestACancelledStreamClosesDespiteADescendantOutsideTheGroup(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not available here")
	}
	pidfile := filepath.Join(t.TempDir(), "escaped.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := local.New().ExecStream(ctx,
		"setsid sleep 30 & echo $! > "+pidfile+"; echo started; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	awaitPIDFile(t, pidfile) // killed at cleanup: nothing here can reach it
	select {
	case event := <-events:
		if event.Kind != remote.ExecStdout || string(event.Data) != "started\n" {
			t.Fatalf("first event is %+v", event)
		}
	case <-time.After(guardTimeout):
		t.Fatal("no output before the cancellation")
	}

	start := time.Now()
	cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for range events {
		}
	}()
	awaitReturn(t, closed)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the channel closed %s after the cancellation", elapsed)
	}
}

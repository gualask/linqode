//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// The screen answers SIGTERM by quitting and takes no context, so a run
// cancelled by a signal it does not answer — SIGHUP — asks it with one, and
// keeps asking until it has returned.
func TestACancelledRunAsksTheScreenToQuit(t *testing.T) {
	terms := make(chan os.Signal, 8)
	signal.Notify(terms, syscall.SIGTERM)
	defer signal.Stop(terms)

	ctx, cancel := context.WithCancel(context.Background())
	stop := quitOnCancel(ctx)
	cancel()
	for range 2 {
		select {
		case <-terms:
		case <-time.After(5 * time.Second):
			stop()
			t.Fatal("no SIGTERM reached the screen after the run was cancelled")
		}
	}
	stop()

	// Stopped means stopped: nothing more is sent once the screen is gone.
	// A signal sent just before stop may still be in flight, so let it land
	// before draining.
	time.Sleep(50 * time.Millisecond)
	drain(terms)
	time.Sleep(300 * time.Millisecond)
	if len(terms) != 0 {
		t.Errorf("%d SIGTERM(s) sent after the screen returned", len(terms))
	}
}

// A screen that returns on its own is asked nothing.
func TestARunThatEndsByItselfSendsNoSignal(t *testing.T) {
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	defer signal.Stop(terms)

	ctx, cancel := context.WithCancel(context.Background())
	quitOnCancel(ctx)()
	cancel()
	select {
	case <-terms:
		t.Error("a SIGTERM was sent for a run that had already ended")
	case <-time.After(300 * time.Millisecond):
	}
}

func drain(signals chan os.Signal) {
	for {
		select {
		case <-signals:
		default:
			return
		}
	}
}

package home

// Tests for watching the daemon: that the stream is only opened once there is
// a project to scope it to, that a change re-reads the table, that news
// arriving mid-read is not lost, and that losing the stream puts the timer
// back where it was.

import (
	"errors"
	"strings"
	"testing"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/status"
)

// watchStream is a hand-built feed standing in for the remote command.
type watchStream struct {
	events  chan operations.Event
	stopped bool
}

func newWatchStream() *watchStream {
	return &watchStream{events: make(chan operations.Event, 16)}
}

func (w *watchStream) feed() operations.Feed {
	return operations.Feed{Events: w.events, Stop: func() { w.stopped = true }}
}

func (w *watchStream) change(container string) {
	w.events <- operations.Event{Kind: operations.EventChange,
		Change: compose.Event{Action: "die", Container: container, ExitCode: "137"}}
}

// watched builds a screen whose service list names a project and whose watch
// hands back the given stream, and drives it to the point where the stream is
// open. reads counts the `ps` round-trips.
func watched(t *testing.T, stream *watchStream) (*Model, *int) {
	t.Helper()
	reads := 0
	screen := New(Config{
		Services: func() ([]compose.Service, error) {
			reads++
			return []compose.Service{{Service: "web", Name: "app-web-1",
				Project: "app", State: "running"}}, nil
		},
		Watch: func(project string) (operations.Feed, error) {
			if project != "app" {
				t.Errorf("watch scoped to %q, want the project from ps", project)
			}
			return stream.feed(), nil
		},
	}, status.New(status.Config{}))
	applyScreen(screen, screen.sampler.due())
	if screen.watch == nil {
		t.Fatal("the stream was not opened after the first service list")
	}
	return screen, &reads
}

// Nothing is watched until `ps` has said what project these containers belong
// to: an unscoped stream would report every container on the host.
func TestWatchingWaitsForTheProjectName(t *testing.T) {
	opened := 0
	screen := New(Config{
		Services: func() ([]compose.Service, error) { return nil, nil },
		Watch: func(string) (operations.Feed, error) {
			opened++
			return newWatchStream().feed(), nil
		},
	}, status.New(status.Config{}))

	applyScreen(screen, screen.sampler.due())
	if opened != 0 {
		t.Fatalf("the stream was opened %d times with no project to scope it to", opened)
	}
}

// While the stream is up the timer steps back to a safety net; losing the
// stream puts it back on the short interval it had before.
func TestWatchingSlowsTheTimerAndLosingItRestoresIt(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)

	if got := screen.sampler.sources[sourceServices].every; got != servicesWatched {
		t.Errorf("interval while watching = %s, want %s", got, servicesWatched)
	}

	close(stream.events)
	screen.handleWatchTick()
	if screen.watch != nil {
		t.Error("the screen kept a stream that had ended")
	}
	if got := screen.sampler.sources[sourceServices].every; got != servicesRefresh {
		t.Errorf("interval after the stream ended = %s, want %s", got, servicesRefresh)
	}
}

// A change re-reads the table, without waiting for the next interval.
func TestAChangeReReadsTheTable(t *testing.T) {
	stream := newWatchStream()
	screen, reads := watched(t, stream)
	before := *reads

	stream.change("app-web-1")
	applyScreen(screen, screen.handleWatchTick())
	if *reads != before+1 {
		t.Errorf("%d reads after a change, want %d", *reads-before, 1)
	}
}

// News that arrives while a read is in flight describes a state that read
// cannot have seen, so another one follows it. Without this, the last event of
// a restart — the one that says it came back up — would be the one lost.
func TestNewsDuringAReadIsNotLost(t *testing.T) {
	stream := newWatchStream()
	screen, reads := watched(t, stream)

	// A read is in flight: the sampler refuses to start another.
	screen.sampler.read(sourceServices)
	before := *reads
	stream.change("app-web-1")
	screen.handleWatchTick()
	if *reads != before {
		t.Fatalf("a second read started while one was in flight")
	}
	if !screen.servicesStale {
		t.Fatal("the change was forgotten rather than remembered")
	}

	// The in-flight read lands: the follow-up goes out.
	applyScreen(screen, screen.Update(servicesSampleMsg{}))
	if *reads != before+1 {
		t.Errorf("%d follow-up reads after the sample landed, want 1", *reads-before)
	}
	if screen.servicesStale {
		t.Error("the screen still thinks it owes a read")
	}
}

// Watching is an optimisation. A host whose daemon refuses the stream keeps
// the timer and says nothing about it: there is nothing an operator could do.
func TestAFailedStreamIsSilentAndLeavesTheTimer(t *testing.T) {
	screen := New(Config{
		Services: func() ([]compose.Service, error) {
			return []compose.Service{{Service: "web", Project: "app"}}, nil
		},
		Watch: func(string) (operations.Feed, error) {
			return operations.Feed{}, errors.New("permission denied")
		},
	}, status.New(status.Config{}))

	applyScreen(screen, screen.sampler.due())
	if screen.watch != nil || screen.watchStarting {
		t.Error("a failed stream was kept")
	}
	if got := screen.sampler.sources[sourceServices].every; got != servicesRefresh {
		t.Errorf("interval after a failed stream = %s, want the timer's %s",
			got, servicesRefresh)
	}
	if status := screen.focused().Status(); strings.Contains(status, "permission denied") {
		t.Errorf("the failure was put in front of the operator: %q", status)
	}
}

// The stream keeps draining while a log view is on screen: that is exactly
// when a container is most likely to change, and its channel must not fill.
func TestTheStreamDrainsWhileAnotherViewIsOnScreen(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)

	cmd, handled := screen.UpdateBackground(watchTickMsg{})
	if !handled || cmd == nil {
		t.Fatal("the stream stopped draining behind another view")
	}
}

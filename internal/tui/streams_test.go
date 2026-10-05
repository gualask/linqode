package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

// counter is a feed whose stops are counted.
type counter struct{ stops int }

func (c *counter) feed() operations.Feed {
	return operations.Feed{Events: make(chan operations.Event), Stop: func() { c.stops++ }}
}

// Quitting returns from the program with every model as it was, so a stream
// that was open is still open unless something stops it: the log view's
// feed, the daemon's stream, the live stats. One whose start was still in
// flight has no owner at all, and is stopped as it lands.
func TestQuittingStopsEveryStreamOpenOrStarting(t *testing.T) {
	var logs, watch, live, script, late counter
	s := newStreams()
	backend := s.wrap(Backend{
		Logs:      func(string, int) (operations.Feed, error) { return logs.feed(), nil },
		Watch:     func(string) (operations.Feed, error) { return watch.feed(), nil },
		LiveStats: func() (operations.Feed, error) { return live.feed(), nil },
		Script:    func(string) (operations.Feed, error) { return script.feed(), nil },
		AdHoc:     func(string) (operations.Feed, error) { return late.feed(), nil },
	})
	if _, err := backend.Logs("web", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Watch("app"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.LiveStats(); err != nil {
		t.Fatal(err)
	}
	// One its owner already stopped is not stopped a second time.
	closed, _ := backend.Script("deploy")
	closed.Stop()

	s.stopAll()
	for name, c := range map[string]*counter{"logs": &logs, "watch": &watch,
		"live stats": &live, "script": &script} {
		if c.stops != 1 {
			t.Errorf("%s stopped %d times, want once", name, c.stops)
		}
	}

	// A start that was in flight when the program returned lands with
	// nobody left to deliver it to.
	feed, _ := backend.AdHoc("uptime")
	if late.stops != 1 {
		t.Errorf("a stream started after quitting stopped %d times, want once", late.stops)
	}
	feed.Stop()
	if late.stops != 1 {
		t.Error("a stream was stopped twice")
	}
}

// A context that ends ends the program the way quitting does: Run returns the
// context's error, and what the session had open on the host is stopped.
func TestACancelledContextEndsTheProgramAndItsStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var watch counter
	opened := make(chan struct{})
	backend := Backend{
		Services: func() ([]compose.Service, error) {
			return []compose.Service{{Service: "web", Name: "app-web-1", Project: "app"}}, nil
		},
		Watch: func(string) (operations.Feed, error) {
			defer close(opened)
			return watch.feed(), nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Info{Target: "deploy@prod"}, backend,
			tea.WithInput(&bytes.Buffer{}), tea.WithOutput(io.Discard))
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the program never opened the stream")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want the context's error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not end the program")
	}
	if watch.stops != 1 {
		t.Errorf("the daemon's stream was stopped %d times, want once", watch.stops)
	}
}

// A capability the session does not have stays absent after wrapping: nil is
// how the screen knows not to offer it.
func TestWrappingKeepsAbsentCapabilitiesAbsent(t *testing.T) {
	backend := newStreams().wrap(Backend{})
	if backend.Logs != nil || backend.Watch != nil || backend.LiveStats != nil ||
		backend.Script != nil || backend.AdHoc != nil || backend.Action != nil {
		t.Error("wrapping gave the session a capability it did not have")
	}
}

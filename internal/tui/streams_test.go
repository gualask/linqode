package tui

import (
	"testing"

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

// A capability the session does not have stays absent after wrapping: nil is
// how the screen knows not to offer it.
func TestWrappingKeepsAbsentCapabilitiesAbsent(t *testing.T) {
	backend := newStreams().wrap(Backend{})
	if backend.Logs != nil || backend.Watch != nil || backend.LiveStats != nil ||
		backend.Script != nil || backend.AdHoc != nil || backend.Action != nil {
		t.Error("wrapping gave the session a capability it did not have")
	}
}

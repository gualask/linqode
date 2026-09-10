package events

// Tests for the feed: what an event is worth reading as, where the selection
// goes when a new one arrives, and what the panel says when it has nothing.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
)

var at = time.Date(2026, 9, 5, 12, 4, 31, 0, time.UTC)

func feed(events ...compose.Event) *Model {
	m := New()
	m.SetWatching(true)
	m.SetServices([]compose.Service{
		{Service: "web", Name: "app-web-1"},
		{Service: "worker", Name: "app-worker-1"},
	})
	for _, event := range events {
		m.Add(event, at)
	}
	m.SetSize(80, 6)
	return m
}

func died(container, code string) compose.Event {
	return compose.Event{At: at, Action: "die", Container: container, ExitCode: code}
}

// The feed names containers the way the table above it does. A row of
// `app-web-1` under a table of `web` is the same thing wearing two names.
func TestTheFeedNamesServicesLikeTheTable(t *testing.T) {
	view := feed(died("app-web-1", "0")).View()
	if !strings.Contains(view, "web") {
		t.Errorf("the service name is missing:\n%s", view)
	}
	if strings.Contains(view, "app-web-1") {
		t.Errorf("the container name was shown instead of the service:\n%s", view)
	}
	if !strings.Contains(view, "12:04:31") {
		t.Errorf("the time is missing:\n%s", view)
	}
}

// A container the project no longer has keeps the name the daemon gave it:
// it is still what happened.
func TestAnUnknownContainerKeepsItsOwnName(t *testing.T) {
	view := feed(died("app-gone-1", "0")).View()
	if !strings.Contains(view, "app-gone-1") {
		t.Errorf("the container the daemon named is missing:\n%s", view)
	}
}

// The exit code is the whole story of a `die`, and it is not visible anywhere
// else on the screen once the row is gone.
func TestExitCodesAreReadRatherThanPrinted(t *testing.T) {
	cases := []struct {
		event compose.Event
		want  string
	}{
		{died("app-web-1", "0"), "exited (0)"},
		{died("app-web-1", ""), "exited (0)"},
		{died("app-web-1", "137"), "killed (137)"},
		{died("app-web-1", "1"), "exited (1)"},
		{compose.Event{At: at, Action: "oom", Container: "app-web-1"}, "out of memory"},
		{compose.Event{At: at, Action: "start", Container: "app-web-1"}, "started"},
		{compose.Event{At: at, Action: "health_status: unhealthy",
			Container: "app-web-1"}, "unhealthy"},
		{compose.Event{At: at, Action: "health_status: healthy",
			Container: "app-web-1"}, "healthy"},
	}
	for _, test := range cases {
		text, _ := describe(test.event)
		if text != test.want {
			t.Errorf("%q read as %q, want %q", test.event.Action, text, test.want)
		}
	}
}

// An action this panel has never heard of is still shown. The daemon adds
// them, and a feed that hides what it does not recognize is worse than one
// that prints a word.
func TestAnUnknownActionIsStillShown(t *testing.T) {
	text, _ := describe(compose.Event{Action: "checkpoint", Container: "app-web-1"})
	if text != "checkpoint" {
		t.Errorf("an unrecognised action read as %q", text)
	}
}

// Newest first, because the panel is a few rows tall and it is the newest
// event that has to be visible without anyone scrolling to it.
func TestNewestFirst(t *testing.T) {
	m := feed()
	m.Add(compose.Event{At: at, Action: "start", Container: "app-web-1"}, at)
	m.Add(died("app-worker-1", "137"), at)

	lines := strings.Split(m.View(), "\n")
	if !strings.Contains(lines[0], "worker") {
		t.Errorf("the newest event is not on top:\n%s", m.View())
	}
}

// The selection follows the newest event while it is on it, and stays on its
// own entry once it has been moved off — the log view's rule, for the same
// reason: something being read must not slide away.
func TestSelectionFollowsTheFeedUntilItIsMoved(t *testing.T) {
	m := feed(died("app-web-1", "1"))
	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d", m.cursor)
	}
	m.Add(compose.Event{At: at, Action: "start", Container: "app-web-1"}, at)
	if m.cursor != 0 {
		t.Errorf("cursor left the newest event on its own: %d", m.cursor)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("down moved the cursor to %d", m.cursor)
	}
	selected := m.entries[m.cursor]
	m.Add(compose.Event{At: at, Action: "stop", Container: "app-web-1"}, at)
	if m.entries[m.cursor] != selected {
		t.Errorf("a new event moved the selection off what was being read")
	}
}

// A hint is a promise. This panel kept advertising `j/k select` after the vim
// aliases were removed from the application, so two of the keys on the footer
// were answered by nothing at all. Moving the selection is the arrows,
// `PgUp`/`PgDn` and `Home`/`End` — the same set as in every other list on the
// screen, and what every terminal already sends is not worth a hint.
func TestTheFeedPromisesNoKeyItDoesNotAnswer(t *testing.T) {
	m := feed(died("app-web-1", "1"), died("app-worker-1", "0"))
	for _, gone := range []string{"j", "k", "g", "G", "l"} {
		before := m.cursor
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(gone)})
		if m.cursor != before {
			t.Errorf("%q still moves the cursor, and the aliases are gone", gone)
		}
	}
	for _, hint := range m.Hints() {
		for _, gone := range []string{"j/k", "g/G"} {
			if strings.Contains(hint.Text, gone) {
				t.Errorf("the footer offers %q, which nothing answers: %q", gone, hint.Text)
			}
		}
	}
}

// The service behind the selected event is what `enter` opens.
func TestSelectedService(t *testing.T) {
	m := feed(died("app-worker-1", "137"))
	if service, ok := m.SelectedService(); !ok || service != "worker" {
		t.Errorf("SelectedService = %q, %v; want worker", service, ok)
	}

	unknown := feed(died("app-gone-1", "0"))
	if service, ok := unknown.SelectedService(); ok {
		t.Errorf("a destroyed container offered logs to open: %q", service)
	}
}

// An empty panel has to say which of two things is true: nothing has
// happened, or nothing is listening.
func TestAnEmptyFeedSaysWhyItIsEmpty(t *testing.T) {
	m := New()
	m.SetSize(80, 4)
	m.SetWatching(true)
	if !strings.Contains(m.View(), "nothing has happened") {
		t.Errorf("a watching feed with no events says: %q", m.View())
	}
	m.SetWatching(false)
	if !strings.Contains(m.View(), "not watching") {
		t.Errorf("a feed with no stream says: %q", m.View())
	}
	// On the panel's own rule, not in the footer: the footer is the keymap,
	// and this is legible there without the panel having focus.
	if !strings.Contains(m.Summary(), "not watching") {
		t.Errorf("the rule does not say the stream is down: %q", m.Summary())
	}
	if m.Status() != "" {
		t.Errorf("the footer carries monitoring again: %q", m.Status())
	}
}

// What the feed has caught rides on its rule beside the watching flag.
func TestTheRuleCountsWhatTheFeedCaught(t *testing.T) {
	m := feed()
	m.SetWatching(true)
	if got := m.Summary(); got != "" {
		t.Errorf("an empty watching feed says %q, want nothing", got)
	}
	m.Add(compose.Event{At: at, Action: "start", Container: "app-web-1"}, at)
	if got := m.Summary(); !strings.Contains(got, "1 event") {
		t.Errorf("Summary() = %q after one event", got)
	}
	m.Add(compose.Event{At: at, Action: "die", Container: "app-web-1"}, at)
	if got := m.Summary(); !strings.Contains(got, "2 events") {
		t.Errorf("Summary() = %q after two", got)
	}
}

// The ring is bounded: a `compose up` on a large project must not grow it
// without limit.
func TestTheFeedIsBounded(t *testing.T) {
	m := feed()
	for range depth + 50 {
		m.Add(compose.Event{At: at, Action: "start", Container: "app-web-1"}, at)
	}
	if len(m.entries) != depth {
		t.Errorf("the feed holds %d events, want at most %d", len(m.entries), depth)
	}
}

// A panel four rows tall shows four events, and the window follows the
// cursor rather than pinning it out of sight.
func TestTheWindowFollowsTheCursor(t *testing.T) {
	m := feed()
	for index := range 10 {
		m.Add(compose.Event{At: at.Add(time.Duration(index) * time.Second),
			Action: "start", Container: "app-web-1"}, at)
	}
	m.SetSize(80, 4)
	if got := len(strings.Split(m.View(), "\n")); got != 4 {
		t.Errorf("a four-row panel drew %d lines", got)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != len(m.entries)-1 {
		t.Fatalf("G left the cursor at %d of %d", m.cursor, len(m.entries))
	}
	// The oldest event is the selected one, so it has to be in the window.
	lines := strings.Split(m.View(), "\n")
	if !strings.Contains(lines[len(lines)-1], m.entries[m.cursor].At.Format("15:04:05")) {
		t.Errorf("the selected event scrolled out of its own window:\n%s", m.View())
	}
}

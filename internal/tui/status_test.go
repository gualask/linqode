package tui

// Unit tests for the status model: Bubble Tea models are pure
// update/view functions, so view logic that stayed untested in the Rust
// reference (a known gap there) is covered here directly.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
)

func services(names ...string) []compose.Service {
	list := make([]compose.Service, len(names))
	for i, name := range names {
		list[i] = compose.Service{
			Service: name,
			Name:    "app-" + name + "-1",
			State:   "running",
			Status:  "Up",
		}
	}
	return list
}

func key(k string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func TestRefreshPreservesSelectionByName(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m.update(servicesMsg{services: services("db", "web", "worker")})
	m.update(key("j")) // select "web"

	// "db" disappeared: the cursor must stay on "web", now at index 0.
	m.update(servicesMsg{services: services("web", "worker")})
	if m.services[m.selected].Service != "web" {
		t.Errorf("selected %q, want web", m.services[m.selected].Service)
	}

	// The selected service disappeared: the cursor clamps into range.
	m.update(servicesMsg{services: services("worker")})
	if m.selected != 0 {
		t.Errorf("selected index %d, want 0", m.selected)
	}
}

func TestFailedRefreshKeepsServicesAndShowsError(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m.update(servicesMsg{services: services("db", "web")})
	m.update(servicesMsg{err: errors.New("connection lost")})

	if len(m.services) != 2 {
		t.Errorf("previous services dropped: %+v", m.services)
	}
	view := m.view()
	if !strings.Contains(view, "connection lost") {
		t.Errorf("error not shown:\n%s", view)
	}
	if !strings.Contains(view, "db") {
		t.Errorf("table not shown:\n%s", view)
	}

	// The next successful refresh clears the error.
	m.update(servicesMsg{services: services("db", "web")})
	if strings.Contains(m.view(), "connection lost") {
		t.Error("error not cleared after a good refresh")
	}
}

func TestSelectionClampsAtBounds(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m.update(servicesMsg{services: services("a", "b")})

	m.update(key("k"))
	if m.selected != 0 {
		t.Errorf("selected %d after k at top, want 0", m.selected)
	}
	m.update(key("j"))
	m.update(key("j"))
	if m.selected != 1 {
		t.Errorf("selected %d after j at bottom, want 1", m.selected)
	}
	m.update(key("g"))
	if m.selected != 0 {
		t.Errorf("selected %d after g, want 0", m.selected)
	}
	m.update(key("G"))
	if m.selected != 1 {
		t.Errorf("selected %d after G, want 1", m.selected)
	}
}

func TestViewStatesWithoutServices(t *testing.T) {
	m := newStatusModel(Info{Target: "deploy@prod"}, nil)
	if view := m.view(); !strings.Contains(view, "loading") {
		t.Errorf("initial view:\n%s", view)
	}
	m.update(servicesMsg{services: nil})
	if view := m.view(); !strings.Contains(view, "no services") {
		t.Errorf("empty view:\n%s", view)
	}
	m2 := newStatusModel(Info{}, nil)
	m2.update(servicesMsg{err: errors.New("boom")})
	if view := m2.view(); !strings.Contains(view, "no data") {
		t.Errorf("error-without-data view:\n%s", view)
	}
}

func TestRestartsColumnAppearsOnlyWithCounts(t *testing.T) {
	m := newStatusModel(Info{Target: "deploy@prod"}, nil)
	m.setSize(120, 20)
	m.update(servicesMsg{services: services("web", "db")})
	if strings.Contains(m.view(), "RESTARTS") {
		t.Errorf("column shown without any count:\n%s", m.view())
	}

	// A refresh whose inspect answered brings the column with it.
	list := services("web", "db")
	compose.ApplyRestarts(list, map[string]int{"app-web-1": 0, "app-db-1": 12})
	m.update(servicesMsg{services: list})

	view := m.view()
	if !strings.Contains(view, "RESTARTS") {
		t.Errorf("column missing:\n%s", view)
	}
	if !strings.Contains(view, "12") {
		t.Errorf("count missing:\n%s", view)
	}
}

// The count is the reason the column exists, so a looping container must
// not read like a healthy one. Asserted on the styles rather than on
// rendered output: tests run without a TTY, where lipgloss drops the colors
// that carry the distinction.
func TestRestartStyleEscalatesWithTheCount(t *testing.T) {
	none, one, looping := restartStyle(0), restartStyle(1), restartStyle(5)
	if none.GetForeground() == one.GetForeground() {
		t.Error("a restart should look different from none")
	}
	if one.GetForeground() == looping.GetForeground() {
		t.Error("a looping container should look different from one restart")
	}
}

func TestAdHocCommandPromptRunsWhatWasTyped(t *testing.T) {
	m := newStatusModel(Info{ComposeDir: "/srv/app"}, nil)
	m.update(servicesMsg{services: services("web")})
	m.update(key("!"))
	if !m.commandPrompt {
		t.Fatal("! should open the prompt")
	}
	// While the prompt is open the keys type instead of acting: `q` must
	// not quit, `j` must not move the selection.
	for _, k := range []string{"q", "j", " ", "-", "h"} {
		if cmd := m.update(key(k)); cmd != nil {
			t.Fatalf("key %q acted while typing", k)
		}
	}
	if m.commandText != "qj -h" {
		t.Errorf("typed text %q", m.commandText)
	}
	if !strings.Contains(m.view(), "$ qj -h") {
		t.Errorf("prompt not shown:\n%s", m.view())
	}

	cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should run the command")
	}
	msg, ok := cmd().(openAdHocMsg)
	if !ok {
		t.Fatalf("got %T", cmd())
	}
	// Sent as typed: what the user writes runs where plain ssh would run
	// it, not inside the compose directory.
	if msg.command != "qj -h" || msg.title != "$ qj -h" {
		t.Errorf("%+v", msg)
	}
	if m.commandPrompt {
		t.Error("prompt should close after running")
	}
}

func TestAdHocPromptCancelsAndRemembers(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m.update(servicesMsg{services: services("web")})

	// An empty command is a no-op, not a remote `sh -c ''`.
	m.update(key("!"))
	if cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("empty command should not run")
	}

	m.update(key("!"))
	m.update(key("d"))
	m.update(key("f"))
	m.update(tea.KeyMsg{Type: tea.KeyBackspace})
	m.update(key("h"))
	m.update(tea.KeyMsg{Type: tea.KeyEnter})

	// Esc cancels without running, and the prompt reopens on the last
	// command so a typo is edited rather than retyped.
	m.update(key("!"))
	if m.commandText != "dh" {
		t.Errorf("prompt reopened with %q, want the last command", m.commandText)
	}
	m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.commandPrompt {
		t.Error("esc should close the prompt")
	}
	if m.update(key("j")); m.selected != 0 {
		t.Error("keys should act again after esc")
	}
}

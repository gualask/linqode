package status

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

// A key must not act on a panel nobody can see; its own stream keeps
// draining, which is what UpdateBackground is for.
func TestBackgroundHandlesTheStreamButNotKeys(t *testing.T) {
	m := New(Config{})
	if _, handled := m.UpdateBackground(key("j")); handled {
		t.Fatal("the table handled a key while hidden")
	}
	if _, handled := m.UpdateBackground(statsTickMsg{}); !handled {
		t.Fatal("the live stream stopped draining while hidden")
	}
}

func TestRefreshPreservesSelectionByName(t *testing.T) {
	m := New(Config{})
	m.SetServices(services("db", "web", "worker"), nil)
	m.Update(key("j")) // select "web"

	// "db" disappeared: the cursor must stay on "web", now at index 0.
	m.SetServices(services("web", "worker"), nil)
	if m.services[m.selected].Service != "web" {
		t.Errorf("selected %q, want web", m.services[m.selected].Service)
	}

	// The selected service disappeared: the cursor clamps into range.
	m.SetServices(services("worker"), nil)
	if m.selected != 0 {
		t.Errorf("selected index %d, want 0", m.selected)
	}
}

func TestFailedRefreshKeepsServicesAndShowsError(t *testing.T) {
	m := New(Config{})
	m.SetServices(services("db", "web"), nil)
	m.SetServices(nil, errors.New("connection lost"))

	if len(m.services) != 2 {
		t.Errorf("previous services dropped: %+v", m.services)
	}
	// The failure is the panel's footer line; the table it belongs to keeps
	// the last good rows above it.
	if !strings.Contains(m.Status(), "connection lost") {
		t.Errorf("error not reported: %q", m.Status())
	}
	if view := m.View(); !strings.Contains(view, "db") {
		t.Errorf("table not shown:\n%s", view)
	}

	// The next successful refresh clears the error.
	m.SetServices(services("db", "web"), nil)
	if strings.Contains(m.View(), "connection lost") {
		t.Error("error not cleared after a good refresh")
	}
}

func TestSelectionClampsAtBounds(t *testing.T) {
	m := New(Config{})
	m.SetServices(services("a", "b"), nil)

	m.Update(key("k"))
	if m.selected != 0 {
		t.Errorf("selected %d after k at top, want 0", m.selected)
	}
	m.Update(key("j"))
	m.Update(key("j"))
	if m.selected != 1 {
		t.Errorf("selected %d after j at bottom, want 1", m.selected)
	}
	m.Update(key("g"))
	if m.selected != 0 {
		t.Errorf("selected %d after g, want 0", m.selected)
	}
	m.Update(key("G"))
	if m.selected != 1 {
		t.Errorf("selected %d after G, want 1", m.selected)
	}
}

func TestViewStatesWithoutServices(t *testing.T) {
	m := New(Config{})
	if view := m.View(); !strings.Contains(view, "loading") {
		t.Errorf("initial view:\n%s", view)
	}
	m.SetServices(nil, nil)
	if view := m.View(); !strings.Contains(view, "no services") {
		t.Errorf("empty view:\n%s", view)
	}
	m2 := New(Config{})
	m2.SetServices(nil, errors.New("boom"))
	if view := m2.View(); !strings.Contains(view, "no data") {
		t.Errorf("error-without-data view:\n%s", view)
	}
}

func TestRestartsColumnAppearsOnlyWithCounts(t *testing.T) {
	m := New(Config{})
	m.SetSize(120, 20)
	m.SetServices(services("web", "db"), nil)
	if strings.Contains(m.View(), "RESTARTS") {
		t.Errorf("column shown without any count:\n%s", m.View())
	}

	// A refresh whose inspect answered brings the column with it.
	list := services("web", "db")
	compose.ApplyRestarts(list, map[string]int{"app-web-1": 0, "app-db-1": 12})
	m.SetServices(list, nil)

	view := m.View()
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

// A panel with no compose behind it must never claim to be loading something
// that is never coming, and must not dress a permanent condition as a refresh
// that failed.
func TestAnUnavailablePanelSaysWhyRatherThanLoading(t *testing.T) {
	m := New(Config{Unavailable: "docker is not installed on this host"})
	m.SetSize(60, 10)

	view := m.View()
	if !strings.Contains(view, "docker is not installed on this host") {
		t.Errorf("the panel does not say why it is empty: %q", view)
	}
	if strings.Contains(view, "loading") {
		t.Errorf("the panel is waiting for a sample that is never coming: %q", view)
	}
	// Every key it could offer needs compose.
	if hints := m.Hints(); len(hints) != 0 {
		t.Errorf("Hints() = %v on a panel where none of them can work", hints)
	}
	if status := m.Status(); !strings.Contains(status, "compose unavailable") {
		t.Errorf("Status() = %q", status)
	}
}

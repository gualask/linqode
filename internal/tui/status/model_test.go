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

func TestUpdateBackgroundKeepsTimersWithoutStartingHiddenFetches(t *testing.T) {
	servicesCalls, statsCalls := 0, 0
	m := New(Config{
		Services: func() ([]compose.Service, error) {
			servicesCalls++
			return nil, nil
		},
		Stats: func() ([]compose.ContainerStats, error) {
			statsCalls++
			return nil, nil
		},
	})

	if cmd, handled := m.UpdateBackground(autoTickMsg{}); !handled || cmd == nil {
		t.Fatal("background auto tick was not rescheduled")
	}
	if cmd, handled := m.UpdateBackground(statsPollMsg{}); !handled || cmd == nil {
		t.Fatal("background stats poll was not rescheduled")
	}
	if servicesCalls != 0 || statsCalls != 0 || m.refreshing || m.statsRefreshing {
		t.Fatal("background timers started a hidden fetch")
	}

	if _, handled := m.UpdateBackground(servicesMsg{services: services("web")}); !handled {
		t.Fatal("in-flight status result was not handled in the background")
	}
	if !m.loaded || len(m.services) != 1 {
		t.Fatal("in-flight status result was not applied")
	}
	if _, handled := m.UpdateBackground(key("j")); handled {
		t.Fatal("status handled a key while hidden")
	}
}

func TestRefreshPreservesSelectionByName(t *testing.T) {
	m := New(Config{})
	m.Update(servicesMsg{services: services("db", "web", "worker")})
	m.Update(key("j")) // select "web"

	// "db" disappeared: the cursor must stay on "web", now at index 0.
	m.Update(servicesMsg{services: services("web", "worker")})
	if m.services[m.selected].Service != "web" {
		t.Errorf("selected %q, want web", m.services[m.selected].Service)
	}

	// The selected service disappeared: the cursor clamps into range.
	m.Update(servicesMsg{services: services("worker")})
	if m.selected != 0 {
		t.Errorf("selected index %d, want 0", m.selected)
	}
}

func TestFailedRefreshKeepsServicesAndShowsError(t *testing.T) {
	m := New(Config{})
	m.Update(servicesMsg{services: services("db", "web")})
	m.Update(servicesMsg{err: errors.New("connection lost")})

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
	m.Update(servicesMsg{services: services("db", "web")})
	if strings.Contains(m.View(), "connection lost") {
		t.Error("error not cleared after a good refresh")
	}
}

func TestSelectionClampsAtBounds(t *testing.T) {
	m := New(Config{})
	m.Update(servicesMsg{services: services("a", "b")})

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
	m.Update(servicesMsg{services: nil})
	if view := m.View(); !strings.Contains(view, "no services") {
		t.Errorf("empty view:\n%s", view)
	}
	m2 := New(Config{})
	m2.Update(servicesMsg{err: errors.New("boom")})
	if view := m2.View(); !strings.Contains(view, "no data") {
		t.Errorf("error-without-data view:\n%s", view)
	}
}

func TestRestartsColumnAppearsOnlyWithCounts(t *testing.T) {
	m := New(Config{})
	m.SetSize(120, 20)
	m.Update(servicesMsg{services: services("web", "db")})
	if strings.Contains(m.View(), "RESTARTS") {
		t.Errorf("column shown without any count:\n%s", m.View())
	}

	// A refresh whose inspect answered brings the column with it.
	list := services("web", "db")
	compose.ApplyRestarts(list, map[string]int{"app-web-1": 0, "app-db-1": 12})
	m.Update(servicesMsg{services: list})

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

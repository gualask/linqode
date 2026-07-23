package tui

// Unit tests for the status model: Bubble Tea models are pure
// Update/View functions, so view logic that stayed untested in the Rust
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

func update(t *testing.T, m statusModel, msg tea.Msg) statusModel {
	t.Helper()
	next, _ := m.Update(msg)
	model, ok := next.(statusModel)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return model
}

func key(k string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func TestRefreshPreservesSelectionByName(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m = update(t, m, servicesMsg{services: services("db", "web", "worker")})
	m = update(t, m, key("j")) // select "web"

	// "db" disappeared: the cursor must stay on "web", now at index 0.
	m = update(t, m, servicesMsg{services: services("web", "worker")})
	if m.services[m.selected].Service != "web" {
		t.Errorf("selected %q, want web", m.services[m.selected].Service)
	}

	// The selected service disappeared: the cursor clamps into range.
	m = update(t, m, servicesMsg{services: services("worker")})
	if m.selected != 0 {
		t.Errorf("selected index %d, want 0", m.selected)
	}
}

func TestFailedRefreshKeepsServicesAndShowsError(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m = update(t, m, servicesMsg{services: services("db", "web")})
	m = update(t, m, servicesMsg{err: errors.New("connection lost")})

	if len(m.services) != 2 {
		t.Errorf("previous services dropped: %+v", m.services)
	}
	view := m.View()
	if !strings.Contains(view, "connection lost") {
		t.Errorf("error not shown:\n%s", view)
	}
	if !strings.Contains(view, "db") {
		t.Errorf("table not shown:\n%s", view)
	}

	// The next successful refresh clears the error.
	m = update(t, m, servicesMsg{services: services("db", "web")})
	if strings.Contains(m.View(), "connection lost") {
		t.Error("error not cleared after a good refresh")
	}
}

func TestSelectionClampsAtBounds(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m = update(t, m, servicesMsg{services: services("a", "b")})

	m = update(t, m, key("k"))
	if m.selected != 0 {
		t.Errorf("selected %d after k at top, want 0", m.selected)
	}
	m = update(t, m, key("j"))
	m = update(t, m, key("j"))
	if m.selected != 1 {
		t.Errorf("selected %d after j at bottom, want 1", m.selected)
	}
	m = update(t, m, key("g"))
	if m.selected != 0 {
		t.Errorf("selected %d after g, want 0", m.selected)
	}
	m = update(t, m, key("G"))
	if m.selected != 1 {
		t.Errorf("selected %d after G, want 1", m.selected)
	}
}

func TestViewStatesWithoutServices(t *testing.T) {
	m := newStatusModel(Info{Target: "deploy@prod"}, nil)
	if view := m.View(); !strings.Contains(view, "loading") {
		t.Errorf("initial view:\n%s", view)
	}
	m = update(t, m, servicesMsg{services: nil})
	if view := m.View(); !strings.Contains(view, "no services") {
		t.Errorf("empty view:\n%s", view)
	}
	m2 := newStatusModel(Info{}, nil)
	m2 = update(t, m2, servicesMsg{err: errors.New("boom")})
	if view := m2.View(); !strings.Contains(view, "no data") {
		t.Errorf("error-without-data view:\n%s", view)
	}
}

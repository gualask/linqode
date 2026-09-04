package status

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case StatsFeedMsg:
		return m.handleStatsFeed(msg)
	case statsTickMsg:
		return m.handleStatsTick()
	case tea.KeyMsg:
		return m.handleStatusKey(msg)
	}
	return nil
}

// UpdateBackground keeps the live stream draining while another view is on
// screen. Nothing else here starts work nobody can see.
func (m *Model) UpdateBackground(msg tea.Msg) (tea.Cmd, bool) {
	switch msg.(type) {
	case StatsFeedMsg, statsTickMsg:
		return m.Update(msg), true
	default:
		return nil, false
	}
}

func selectedServiceIndex(selected int, previous, refreshed []compose.Service) int {
	if selected >= len(previous) {
		return selected
	}
	name := previous[selected].Name
	for i, service := range refreshed {
		if service.Name == name {
			return i
		}
	}
	return selected
}

func (m *Model) handleStatsFeed(msg StatsFeedMsg) tea.Cmd {
	m.statsStarting = false
	if msg.Err != nil {
		m.statsErr = msg.Err.Error()
		return nil
	}
	feed := msg.Feed
	m.statsFeed = &feed
	if m.stats == nil {
		m.stats = map[string]compose.ContainerStats{}
	}
	return statsTick()
}

func (m *Model) handleStatsTick() tea.Cmd {
	if m.statsFeed == nil {
		return nil
	}
	if !m.drainStats() {
		return statsTick()
	}

	// The stream stopped on its own. Keep the last samples on screen, let
	// the screen's sampling take the columns back, and end the discontinuous
	// history.
	m.statsFeed.Stop()
	m.statsFeed = nil
	m.history = nil
	return nil
}

// handleStatusKey answers the keys the table owns: moving the cursor, its own
// refresh, its own live mode. Quitting, opening, and the modals belong to the
// screen composing this panel.
func (m *Model) handleStatusKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.selected = 0
	case "G", "end":
		m.selected = max(0, len(m.services)-1)
	case "a":
		return m.toggleLive()
	}
	return nil
}

// SelectedService is the service the cursor is on, for the screen to open or
// to build an action menu around. It reports false on an empty table.
func (m *Model) SelectedService() (string, bool) {
	if m.selected >= len(m.services) {
		return "", false
	}
	return m.services[m.selected].Service, true
}

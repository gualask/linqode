package status

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case servicesMsg:
		return m.handleServices(msg)
	case statsSampleMsg:
		return m.handleStatsSample(msg)
	case statsPollMsg:
		return tea.Batch(m.refreshStats(), statsPollTick())
	case StatsFeedMsg:
		return m.handleStatsFeed(msg)
	case statsTickMsg:
		return m.handleStatsTick()
	case hostMsg:
		return m.handleHost(msg)
	case autoTickMsg:
		return tea.Batch(m.Refresh(), m.refreshHost(), autoTick())
	case tea.KeyMsg:
		return m.handleStatusKey(msg)
	}
	return nil
}

// UpdateBackground handles status-owned asynchronous results and keeps its
// timers alive while another view is visible, without starting hidden fetches.
func (m *Model) UpdateBackground(msg tea.Msg) (tea.Cmd, bool) {
	switch msg.(type) {
	case autoTickMsg:
		return autoTick(), true
	case statsPollMsg:
		return statsPollTick(), true
	case servicesMsg, hostMsg, StatsFeedMsg, statsTickMsg, statsSampleMsg:
		return m.Update(msg), true
	default:
		return nil, false
	}
}

func (m *Model) handleServices(msg servicesMsg) tea.Cmd {
	m.refreshing = false
	m.loaded = true
	if msg.err != nil {
		m.errText = msg.err.Error()
		return nil
	}

	// Keep the cursor on the same service across refreshes; if it is gone,
	// stay at the same position, clamped into range.
	m.selected = selectedServiceIndex(m.selected, m.services, msg.services)
	m.services = msg.services
	m.selected = min(m.selected, max(0, len(m.services)-1))
	m.errText = ""
	return nil
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

func (m *Model) handleStatsSample(msg statsSampleMsg) tea.Cmd {
	m.statsRefreshing = false
	if msg.err != nil {
		// Keep the last values, as with a failed host sample. Refresh failures
		// remain the table's actionable error.
		m.statsErr = msg.err.Error()
		return nil
	}
	m.statsErr = ""
	m.applySample(msg.stats)
	return nil
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
	// the soft poll take the columns back, and end the discontinuous history.
	m.statsFeed.Stop()
	m.statsFeed = nil
	m.history = nil
	return m.refreshStats()
}

func (m *Model) handleHost(msg hostMsg) tea.Cmd {
	m.metricsRefreshing = false
	if msg.err != nil {
		// Keep the last sample visible and mark it stale.
		m.metricsStale = true
		return nil
	}
	m.metrics = msg.metrics
	m.metricsLoaded = true
	m.metricsStale = false
	return nil
}

func (m *Model) handleStatusKey(msg tea.KeyMsg) tea.Cmd {
	if m.commandPrompt {
		return m.handleCommandKey(msg)
	}
	if m.menu != nil {
		return m.handleMenuKey(msg)
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.selected = 0
	case "G", "end":
		m.selected = max(0, len(m.services)-1)
	case "r":
		return m.Refresh()
	case "enter", "l":
		return m.openSelectedLogs()
	case "c":
		m.openActionMenu()
	case "x":
		m.openScriptMenu()
	case "!":
		m.commandPrompt, m.commandText = true, m.lastCommand
		m.errText = ""
	case "a":
		return m.toggleLive()
	}
	return nil
}

func (m *Model) openSelectedLogs() tea.Cmd {
	if m.selected >= len(m.services) {
		return nil
	}
	service := m.services[m.selected].Service
	return openLogs("logs: "+service, service)
}

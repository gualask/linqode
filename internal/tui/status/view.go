package status

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return theme.Green
	case "restarting", "paused", "created":
		return theme.Yellow
	case "exited", "dead":
		return theme.Red
	default:
		return lipgloss.NewStyle()
	}
}

func restartStyle(n int) lipgloss.Style {
	switch {
	case n >= 5:
		return theme.Red
	case n > 0:
		return theme.Yellow
	default:
		return theme.Dim
	}
}

func healthStyle(health string) lipgloss.Style {
	switch health {
	case "":
		return theme.Dim
	case "healthy":
		return theme.Green
	case "starting":
		return theme.Yellow
	case "unhealthy":
		return theme.Red
	default:
		return lipgloss.NewStyle()
	}
}

// Title names the panel in the screen's layout.
func (m *Model) Title() string { return "services" }

// SetFocus records whether the keys are talking to this panel. It changes how
// the cursor row is drawn, never what it does.
func (m *Model) SetFocus(focused bool) { m.focused = focused }

// selectionStyle marks the cursor row. Without focus the row keeps its place
// with a quiet fill rather than the reverse bar, so two panels are never both
// dressed as the one being acted on.
func (m *Model) selectionStyle() lipgloss.Style {
	if m.focused {
		return theme.Reverse
	}
	return theme.SelectedIdle
}

// Hints are the keys the table itself answers to. The screen adds its own and
// decides which survive a narrow terminal.
func (m *Model) Hints() []panel.Hint {
	hints := []panel.Hint{{Text: "enter logs", Drop: 2}}
	if m.liveStats {
		live := "a live"
		if m.liveActive() {
			live = "a live off"
		}
		hints = append(hints, panel.Hint{Text: live, Drop: 5})
	}
	return hints
}

// Status is the panel's half of the footer: how many services it is showing,
// or the failure that kept it from showing them.
func (m *Model) Status() string {
	if m.errText != "" {
		return theme.Red.Render(" " + strings.ReplaceAll(m.errText, "\n", " · "))
	}
	text := fmt.Sprintf(" %d services", len(m.services))
	if m.statsErr != "" {
		text += theme.Red.Render("  ·  stats: " + m.statsErr)
	}
	return text
}

// View is the panel's content, drawn to the size the screen last gave it. The
// live stats strip, when it is open, shares that area with the table: it is
// the same panel looking closer at its own rows.
func (m *Model) View() string {
	live := ""
	tableHeight := m.height
	if m.liveActive() {
		liveHeight := min(m.liveHeight(), max(m.height-3, 0))
		if liveHeight > 0 {
			tableHeight = m.height - liveHeight
			live = "\n" + m.renderLivePanel(m.width)
		}
	}
	var b strings.Builder
	switch {
	case len(m.services) == 0 && !m.loaded:
		b.WriteString(theme.Dim.Render("  (loading services…)"))
	case len(m.services) == 0 && m.errText != "":
		b.WriteString(theme.Dim.Render("  (no data — see error below)"))
	case len(m.services) == 0:
		b.WriteString(theme.Dim.Render("  (no services in this compose project)"))
	default:
		m.renderTable(&b, m.width, tableHeight)
	}
	return strings.TrimRight(b.String(), "\n") + live
}

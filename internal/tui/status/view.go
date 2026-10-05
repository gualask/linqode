package status

import (
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
	// One predicate governs both this panel's keys and the screen's `c`: what
	// is on offer here is the same question asked twice.
	if !m.OffersServiceKeys() {
		return nil
	}
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

// Status is the panel's half of the footer, and it is empty while nothing is
// wrong.
//
// It used to count the services, which the panel's own rule now says better:
// `4 running · 1 exited · 1 unhealthy` is the same total and the breakdown
// besides. Saying it twice on one screen left the footer repeating what was
// two rows above it, so what is left here is what has no other place — the
// reasons this panel is not showing what it should.
func (m *Model) Status() string {
	if m.unavailable != "" {
		// Yellow, not red: nothing failed here. The host is what it is, and
		// this is a thing the operator may be able to change on the server.
		return theme.Yellow.Render(" compose unavailable")
	}
	if m.errText != "" {
		return theme.Red.Render(" " + panel.Plain(strings.ReplaceAll(m.errText, "\n", " · ")))
	}
	if m.statsErr != "" {
		return theme.Red.Render(" stats: " + panel.Plain(m.statsErr))
	}
	return ""
}

// View is the panel's content, drawn to the size the screen last gave it. The
// live stats strip, when it is open, shares that area with the table: it is
// the same panel looking closer at its own rows.
func (m *Model) View() string {
	live := ""
	tableHeight := m.height
	if liveHeight := m.liveShare(); liveHeight > 0 {
		tableHeight = m.height - liveHeight
		live = "\n" + m.renderLivePanel(m.width)
	}
	disk := ""
	if len(m.diskUsage) > 0 && m.DiskUsageRoom() {
		tableHeight -= m.diskHeight()
		disk = "\n" + m.renderDisk(m.width)
	}
	var b strings.Builder
	switch {
	case m.unavailable != "":
		// Said here, where the table would have been, because this is the
		// answer to the question an empty panel provokes. Once, at connect —
		// not rediscovered on every safety-net interval for the life of the
		// session, which is what this replaces.
		b.WriteString(theme.Yellow.Render("  compose is unavailable on this host") + "\n")
		b.WriteString(theme.Dim.Render("  "+panel.Plain(m.unavailable)) + "\n")
		b.WriteString(theme.Dim.Render("  the machine's own readings are unaffected"))
	case len(m.services) == 0 && !m.loaded:
		b.WriteString(theme.Dim.Render("  (loading services…)"))
	case len(m.services) == 0 && m.errText != "":
		b.WriteString(theme.Dim.Render("  (no data — see error below)"))
	case len(m.services) == 0:
		b.WriteString(theme.Dim.Render("  (no services in this compose project)"))
	default:
		m.renderTable(&b, m.width, tableHeight)
	}
	return strings.TrimRight(b.String(), "\n") + live + disk
}

// liveShare is the rows the live panel takes from the table while it is open:
// what it wants, leaving the table at least its heading and two rows.
func (m *Model) liveShare() int {
	if !m.liveActive() {
		return 0
	}
	return min(m.liveHeight(), max(m.height-3, 0))
}

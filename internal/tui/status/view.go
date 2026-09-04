package status

import (
	"fmt"
	"slices"
	"strings"
	"time"

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

func (m *Model) View() string {
	var b strings.Builder
	// Both header lines are clipped rather than trusted to fit: a line
	// wider than the terminal wraps, and everything below it shifts down by
	// a row for as long as the reading stays wide.
	clip := func(line string) string {
		if m.width <= 0 {
			return line
		}
		return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
	}
	var title strings.Builder
	title.WriteString(theme.Bold.Render(" linqode "))
	title.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		title.WriteString("  ")
		title.WriteString(theme.Cyan.Render(m.info.ComposeDir))
	}
	if summary := m.projectSummary(); summary != "" {
		title.WriteString("  " + summary)
	}
	b.WriteString(clip(title.String()) + "\n")
	headerLines := 2
	if line := m.renderHostLine(); line != "" {
		b.WriteString(clip(line) + "\n")
		headerLines++
	}
	// A blank line, not a rule: the table draws its own heading as a band
	// across the width, so a rule here would be a second separator stacked
	// on the first. What the header block needs from this line is air.
	b.WriteString("\n")
	bodyHeight := max(m.height-headerLines-1, 1)
	fit := func(style lipgloss.Style) lipgloss.Style {
		if m.height <= 0 {
			return style
		}
		return style.Height(bodyHeight).MaxHeight(bodyHeight)
	}
	body := fit(lipgloss.NewStyle()).Render(m.renderBody(m.width, bodyHeight))
	b.WriteString(body + "\n" + m.footer())
	return b.String()
}

func (m *Model) renderBody(width, height int) string {
	if m.menu != nil {
		return m.renderMenu(width, height)
	}
	live := ""
	tableHeight := height
	if m.liveActive() {
		liveHeight := min(m.liveHeight(), max(height-3, 0))
		if liveHeight > 0 {
			tableHeight = height - liveHeight
			live = "\n" + m.renderLivePanel(width)
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
		m.renderTable(&b, width, tableHeight-1)
	}
	return strings.TrimRight(b.String(), "\n") + live
}

// footerHints is what the status screen answers to. The order of the Drop
// values is the order these are given up in when the line does not fit; the
// dropping itself belongs to the footer, not to this view, and lives in
// internal/tui/panel.
func (m *Model) footerHints(width int) string {
	hints := []panel.Hint{{Text: "enter logs", Drop: 2}, {Text: "c actions", Drop: 3},
		{Text: "x scripts", Drop: 6}, {Text: "! run", Drop: 4},
		{Text: "r refresh", Drop: 1}, {Text: "q quit", Drop: 0}}
	if m.liveStats {
		live := "a live"
		if m.liveActive() {
			live = "a live off"
		}
		hints = slices.Insert(hints, 4, panel.Hint{Text: live, Drop: 5})
	}
	return panel.JoinHints(hints, width)
}

func (m *Model) footer() string {
	var text string
	switch {
	case m.commandPrompt:
		text = " $ " + m.commandText + "▏" + theme.Dim.Render("  enter run · esc cancel")
	case m.menu != nil:
		text = theme.Dim.Render(" j/k select · enter run · esc cancel")
	case m.errText != "":
		text = theme.Red.Render(" " + strings.ReplaceAll(m.errText, "\n", " · "))
	default:
		var b strings.Builder
		b.WriteString(fmt.Sprintf(" %d services", len(m.services)))
		if m.statsErr != "" {
			b.WriteString(theme.Red.Render("  ·  stats: " + m.statsErr))
		}
		budget := 0
		if m.width > 0 {
			budget = m.width - lipgloss.Width(b.String()) - len("  ·  ")
		}
		b.WriteString(theme.Dim.Render("  ·  " + m.footerHints(budget)))
		text = b.String()
	}
	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(text)
	}
	return text
}

func usageStyle(percent float64) lipgloss.Style {
	switch {
	case percent >= 90:
		return theme.Red
	case percent >= 75:
		return theme.Yellow
	default:
		return theme.Green
	}
}

func loadStyle(perCPU float64) lipgloss.Style {
	switch {
	case perCPU >= 1:
		return theme.Red
	case perCPU >= 0.7:
		return theme.Yellow
	default:
		return theme.Green
	}
}

func formatKB(kb uint64) string {
	units := []string{"K", "M", "G", "T", "P"}
	value, unit := float64(kb), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%s", value, units[unit])
	}
	return fmt.Sprintf("%.1f%s", value, units[unit])
}

func formatUptime(duration time.Duration) string {
	switch {
	case duration >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(duration.Hours())/24, int(duration.Hours())%24)
	case duration >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(duration.Hours()), int(duration.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	}
}

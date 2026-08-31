package status

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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
	b.WriteString(theme.Bold.Render(" linqode "))
	b.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		b.WriteString("  ")
		b.WriteString(theme.Cyan.Render(m.info.ComposeDir))
	}
	b.WriteString("\n")
	sidebar := m.sidebarOn()
	headerLines := 2
	if !sidebar {
		if line := m.renderHostLine(); line != "" {
			b.WriteString(line + "\n")
			headerLines++
		}
	}
	b.WriteString("\n")
	bodyHeight := max(m.height-headerLines-1, 1)
	bodyWidth := m.width
	if sidebar {
		bodyWidth = m.width - sidebarWidth
	}
	fit := func(style lipgloss.Style) lipgloss.Style {
		if m.height <= 0 {
			return style
		}
		return style.Height(bodyHeight).MaxHeight(bodyHeight)
	}
	body := fit(lipgloss.NewStyle()).Render(m.renderBody(bodyWidth, bodyHeight))
	if sidebar {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			fit(lipgloss.NewStyle().Width(bodyWidth)).Render(body),
			fit(lipgloss.NewStyle().Width(sidebarInner)).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("8")).PaddingLeft(1).
				Render(m.renderSystemPanel()))
	}
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

type footerHint struct {
	text string
	drop int
}

func (m *Model) footerHints(width int) string {
	hints := []footerHint{{"enter logs", 2}, {"c actions", 3}, {"x scripts", 6},
		{"! run", 4}, {"r refresh", 1}, {"q quit", 0}}
	if m.liveStats {
		live := "a live"
		if m.liveActive() {
			live = "a live off"
		}
		hints = slices.Insert(hints, 4, footerHint{live, 5})
	}
	for {
		texts := make([]string, len(hints))
		for index, hint := range hints {
			texts[index] = hint.text
		}
		joined := strings.Join(texts, " · ")
		if width <= 0 || len(hints) == 1 || lipgloss.Width(joined) <= width {
			return joined
		}
		worst := 0
		for index, hint := range hints {
			if hint.drop >= hints[worst].drop {
				worst = index
			}
		}
		hints = slices.Delete(hints, worst, worst+1)
	}
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

func (m *Model) renderHostLine() string {
	if !m.metricsLoaded {
		return ""
	}
	metrics := m.metrics
	var parts []string
	if metrics.HasLoad() {
		parts = append(parts, loadStyle(metrics.LoadPerCPU()).Render(fmt.Sprintf("load %.2f", metrics.Load1))+
			theme.Dim.Render(fmt.Sprintf(" %.2f/cpu", metrics.LoadPerCPU())))
	}
	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		parts = append(parts, fmt.Sprintf("mem %s/%s %s", formatKB(metrics.MemUsedKB()),
			formatKB(metrics.MemTotalKB), usageStyle(percent).Render(fmt.Sprintf("%.0f%%", percent))))
	}
	if metrics.DiskTotalKB > 0 {
		percent := metrics.DiskUsedPercent()
		parts = append(parts, fmt.Sprintf("disk %s/%s %s", formatKB(metrics.DiskUsedKB),
			formatKB(metrics.DiskTotalKB), usageStyle(percent).Render(fmt.Sprintf("%.0f%%", percent))))
	}
	if metrics.Uptime > 0 {
		parts = append(parts, theme.Dim.Render("up "+formatUptime(metrics.Uptime)))
	}
	if len(parts) == 0 {
		return ""
	}
	line := " " + strings.Join(parts, "  ")
	if m.metricsStale {
		line += theme.Dim.Render("  (stale)")
	}
	return line
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

package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	boldStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	cyanStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	blueStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	magentaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	reverseStyle = lipgloss.NewStyle().Reverse(true)
	matchStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))
)

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return greenStyle
	case "restarting", "paused", "created":
		return yellowStyle
	case "exited", "dead":
		return redStyle
	default:
		return lipgloss.NewStyle()
	}
}

func restartStyle(n int) lipgloss.Style {
	switch {
	case n >= 5:
		return redStyle
	case n > 0:
		return yellowStyle
	default:
		return dimStyle
	}
}

func healthStyle(health string) lipgloss.Style {
	switch health {
	case "":
		return dimStyle
	case "healthy":
		return greenStyle
	case "starting":
		return yellowStyle
	case "unhealthy":
		return redStyle
	default:
		return lipgloss.NewStyle()
	}
}

func (m *statusModel) view() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render(" linqode "))
	b.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		b.WriteString("  ")
		b.WriteString(cyanStyle.Render(m.info.ComposeDir))
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

func (m *statusModel) renderBody(width, height int) string {
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
		b.WriteString(dimStyle.Render("  (loading services…)"))
	case len(m.services) == 0 && m.errText != "":
		b.WriteString(dimStyle.Render("  (no data — see error below)"))
	case len(m.services) == 0:
		b.WriteString(dimStyle.Render("  (no services in this compose project)"))
	default:
		m.renderTable(&b, width, tableHeight-1)
	}
	return strings.TrimRight(b.String(), "\n") + live
}

type footerHint struct {
	text string
	drop int
}

func (m *statusModel) footerHints(width int) string {
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

func (m *statusModel) footer() string {
	var text string
	switch {
	case m.commandPrompt:
		text = " $ " + m.commandText + "▏" + dimStyle.Render("  enter run · esc cancel")
	case m.menu != nil:
		text = dimStyle.Render(" j/k select · enter run · esc cancel")
	case m.errText != "":
		text = redStyle.Render(" " + strings.ReplaceAll(m.errText, "\n", " · "))
	default:
		var b strings.Builder
		b.WriteString(fmt.Sprintf(" %d services", len(m.services)))
		if m.statsErr != "" {
			b.WriteString(redStyle.Render("  ·  stats: " + m.statsErr))
		}
		budget := 0
		if m.width > 0 {
			budget = m.width - lipgloss.Width(b.String()) - len("  ·  ")
		}
		b.WriteString(dimStyle.Render("  ·  " + m.footerHints(budget)))
		text = b.String()
	}
	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(text)
	}
	return text
}

func (m *statusModel) renderHostLine() string {
	if !m.metricsLoaded {
		return ""
	}
	metrics := m.metrics
	var parts []string
	if metrics.HasLoad() {
		parts = append(parts, loadStyle(metrics.LoadPerCPU()).Render(fmt.Sprintf("load %.2f", metrics.Load1))+
			dimStyle.Render(fmt.Sprintf(" %.2f/cpu", metrics.LoadPerCPU())))
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
		parts = append(parts, dimStyle.Render("up "+formatUptime(metrics.Uptime)))
	}
	if len(parts) == 0 {
		return ""
	}
	line := " " + strings.Join(parts, "  ")
	if m.metricsStale {
		line += dimStyle.Render("  (stale)")
	}
	return line
}

func usageStyle(percent float64) lipgloss.Style {
	switch {
	case percent >= 90:
		return redStyle
	case percent >= 75:
		return yellowStyle
	default:
		return greenStyle
	}
}

func loadStyle(perCPU float64) lipgloss.Style {
	switch {
	case perCPU >= 1:
		return redStyle
	case perCPU >= 0.7:
		return yellowStyle
	default:
		return greenStyle
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

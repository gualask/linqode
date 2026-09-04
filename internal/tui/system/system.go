package system

// The machine's state: what the band shows in one row, and what the view
// behind it shows when there is a screen to spend.
//
// The model holds a sample rather than fetching one. Who samples, and how
// often, is the screen's business — the same sample feeds the band and the
// view, and a panel that owned its own timer would be one more thing to keep
// in step with the rest of the refresh.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

type Model struct {
	// metrics is the last good sample. A failed one keeps it on screen and
	// flags it stale, the same way a failed service refresh keeps the last
	// good table: a reading that is a few seconds old still says more than
	// a blank.
	metrics host.Metrics
	loaded  bool
	stale   bool

	focused       bool
	width, height int
}

func New() *Model { return &Model{} }

// SetSample applies one host sample. A failed sample marks what is on screen
// stale instead of clearing it; the error itself is not shown, because the
// staleness is what an operator can act on.
func (m *Model) SetSample(metrics host.Metrics, err error) {
	if err != nil {
		m.stale = true
		return
	}
	m.metrics, m.loaded, m.stale = metrics, true, false
}

// Metrics is the last good sample, for a caller that needs a number this
// panel happens to hold — the container readings borrow the machine's memory
// as the ceiling for a container that has no limit of its own.
func (m *Model) Metrics() host.Metrics { return m.metrics }

// Title names the view in its top rule. The band has no rule to put it in.
func (m *Model) Title() string { return "system" }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }
func (m *Model) SetFocus(focused bool)     { m.focused = focused }

// labelStyle draws the band's `host` label, which is where its focus shows:
// the band is one row and has no border to color, and giving it one would
// cost the row a third of its width.
func (m *Model) labelStyle() lipgloss.Style {
	if m.focused {
		return theme.TitleFocus
	}
	return theme.TitleIdle
}

// Hints are the keys the band and the view answer to.
func (m *Model) Hints() []panel.Hint {
	return []panel.Hint{{Text: "enter system", Drop: 2}}
}

// Status is this panel's half of the footer.
func (m *Model) Status() string {
	if !m.loaded {
		return theme.Dim.Render(" no host sample")
	}
	parts := []string{}
	if m.metrics.CPUs > 0 {
		parts = append(parts, fmt.Sprintf("%d cores", m.metrics.CPUs))
	}
	if m.metrics.Uptime > 0 {
		parts = append(parts, "up "+formatUptime(m.metrics.Uptime))
	}
	text := " host"
	if len(parts) > 0 {
		text += "  " + strings.Join(parts, "  ·  ")
	}
	if m.stale {
		text += theme.Dim.Render("  (stale)")
	}
	return text
}

func (m *Model) Update(tea.Msg) tea.Cmd { return nil }

// detailBar is the width of the view's gauges. Wider than the band's, which
// shares its row with two other meters, but not the whole line: past thirty
// cells a bar adds resolution nobody reads.
const detailBar = 30

// labelWidth aligns the rows' first column.
const labelWidth = 8

// View is the system view: the same readings the band carries, with the room
// to print what the band has to leave out — the other load averages, what is
// available rather than only what is used.
//
// It is deliberately thin for now. Everything that makes it worth opening —
// per-core CPU, swap, every filesystem, pressure, temperatures, GPU, the
// processes behind them — arrives in the phases after this one, as rows here
// rather than as boxes on the home.
func (m *Model) View() string {
	if !m.loaded {
		return theme.Dim.Render("  (waiting for the first host sample…)")
	}
	var rows []string
	metrics := m.metrics

	if metrics.HasLoad() {
		perCPU := metrics.LoadPerCPU()
		rows = append(rows, m.row("load", perCPU*100, loadStyle(perCPU),
			fmt.Sprintf("%.2f  %.2f  %.2f", metrics.Load1, metrics.Load5, metrics.Load15)+
				theme.Dim.Render(fmt.Sprintf("   over %d cores", metrics.CPUs))))
	}
	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		rows = append(rows, m.row("memory", percent, theme.Usage(percent),
			fmt.Sprintf("%s used", formatKB(metrics.MemUsedKB()))+
				theme.Dim.Render(fmt.Sprintf("   %s available of %s",
					formatKB(metrics.MemAvailableKB), formatKB(metrics.MemTotalKB)))))
	}
	if metrics.DiskTotalKB > 0 {
		percent := metrics.DiskUsedPercent()
		free := metrics.DiskTotalKB - metrics.DiskUsedKB
		rows = append(rows, m.row("disk /", percent, theme.Usage(percent),
			fmt.Sprintf("%s used", formatKB(metrics.DiskUsedKB))+
				theme.Dim.Render(fmt.Sprintf("   %s free of %s",
					formatKB(free), formatKB(metrics.DiskTotalKB)))))
	}
	if metrics.Uptime > 0 {
		rows = append(rows, fmt.Sprintf(" %-*s %s", labelWidth, "uptime",
			formatUptime(metrics.Uptime)))
	}
	if m.stale {
		rows = append(rows, "", theme.Dim.Render(
			"  the last sample failed — these readings are the ones before it"))
	}
	return strings.Join(rows, "\n")
}

// row is one reading: its name, its gauge, and the numbers the gauge cannot
// carry. The bar shows the percentage, so no row prints one as text.
func (m *Model) row(label string, percent float64, style lipgloss.Style, text string) string {
	width := detailBar
	if m.width > 0 {
		width = min(max(m.width-labelWidth-40, meterMinBar), detailBar)
	}
	return fmt.Sprintf(" %-*s [%s]  %s", labelWidth, label,
		styledBar(percent, width, style), text)
}

// loadStyle colors the load meter. Per-CPU load is the normalization that
// makes 1.0 mean "full" on any machine, so the thresholds are the same shape
// as the usage ones without being percentages of anything.
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

package status

import (
	"fmt"
	"strings"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/tui/theme"
)

// statsCells are a container's CPU, memory and I/O readings, dimmed
// placeholders until its first sample arrives — a container can appear in
// `ps` before stats have been taken for it. The I/O pair is only asked for
// when the terminal has the width for it; see ioColumns.
func (m *Model) statsCells(container string) []cell {
	blank := cell{text: "-", style: theme.Dim}
	stats, ok := m.stats[container]
	if !ok {
		cells := []cell{blank, blank}
		if m.ioColumns() {
			cells = append(cells, blank, blank)
		}
		return cells
	}
	cpu := cell{text: stats.CPUPerc}
	if percent, ok := stats.CPUPercent(); ok {
		cpu.style = usageStyle(percent)
	}
	mem := cell{text: stats.MemAmount()}
	if percent, ok := stats.MemPercent(); ok {
		mem.style = usageStyle(percent)
	}
	cells := []cell{cpu, mem}
	if m.ioColumns() {
		// The I/O pairs are totals since the container started, so there is
		// no threshold to color them against. Dimming keeps the eye on CPU
		// and MEM, which are the readings that can actually be alarming.
		cells = append(cells,
			cell{text: stats.NetAmount(), style: theme.Dim},
			cell{text: stats.BlockAmount(), style: theme.Dim})
	}
	return cells
}

func (m *Model) liveRows() []compose.Service {
	var rows []compose.Service
	for _, service := range m.services {
		if _, ok := m.stats[service.Name]; ok {
			rows = append(rows, service)
			if len(rows) == liveMaxRows {
				break
			}
		}
	}
	return rows
}

func (m *Model) liveHeight() int {
	if !m.liveActive() {
		return 0
	}
	return 2 + max(len(m.liveRows()), 1)
}

func (m *Model) renderLivePanel(width int) string {
	rows := m.liveRows()
	title := m.liveTitle()
	rule := theme.Dim.Render(" ──" + title + strings.Repeat("─", max(width-len(title)-4, 0)))
	if len(rows) == 0 {
		return rule + "\n" + theme.Dim.Render("  "+m.emptyLiveHint())
	}
	return rule + "\n" + strings.Join(m.renderLiveRows(rows, width), "\n")
}

func (m *Model) liveTitle() string {
	if m.statsStarting {
		return " live · starting… "
	}
	var samples int
	for _, series := range m.history {
		samples = max(samples, len(series))
	}
	if samples > 0 {
		return fmt.Sprintf(" live · 1s · %d samples ", samples)
	}
	return " live "
}

func (m *Model) emptyLiveHint() string {
	if m.statsStarting {
		return "(waiting for the first sample…)"
	}
	return "(no running containers to sample)"
}

func (m *Model) renderLiveRows(rows []compose.Service, width int) []string {
	nameWidth := 0
	for _, service := range rows {
		nameWidth = max(nameWidth, len(service.Service))
	}
	const cpuWidth, memWidth, peakWidth = 7, 9, 11
	fixed := 1 + nameWidth + 2 + cpuWidth + 2 + memWidth + 2 + peakWidth + 1
	sparkWidth := width - fixed - 2
	if sparkWidth < 8 {
		sparkWidth = 0
	}
	lines := make([]string, 0, len(rows))
	for _, service := range rows {
		lines = append(lines, m.renderLiveRow(service, nameWidth, sparkWidth))
	}
	return lines
}

func (m *Model) renderLiveRow(service compose.Service, nameWidth, sparkWidth int) string {
	const cpuWidth, memWidth = 7, 9
	stats := m.stats[service.Name]
	series := m.history[service.Name]
	cpuText := fmt.Sprintf("%*s", cpuWidth, stats.CPUPerc)
	if percent, ok := stats.CPUPercent(); ok {
		cpuText = usageStyle(percent).Render(cpuText)
	}
	line := fmt.Sprintf(" %-*s  %s", nameWidth, service.Service, cpuText)
	if sparkWidth > 0 {
		line += "  " + theme.Cyan.Render(sparkline(series, sparkWidth))
	}
	line += fmt.Sprintf("  %*s", memWidth, stats.MemAmount())
	if peak := peakOf(series); peak > 0 {
		line += theme.Dim.Render(fmt.Sprintf("  peak %5.1f%%", peak))
	}
	return line
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

const flatBand = 0.05

func sparkline(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	peak := peakOf(values)
	flat := peak <= 0 || peak-lowOf(values) <= peak*flatBand
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	for _, value := range values {
		level := 0
		if !flat {
			level = int(value/peak*float64(len(sparkRunes)-1) + 0.5)
			level = min(max(level, 0), len(sparkRunes)-1)
		}
		b.WriteRune(sparkRunes[level])
	}
	return b.String()
}

func lowOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	low := values[0]
	for _, value := range values {
		low = min(low, value)
	}
	return low
}

func peakOf(values []float64) float64 {
	peak := 0.0
	for _, value := range values {
		peak = max(peak, value)
	}
	return peak
}

package status

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/tui/spark"
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
		cpu.style = theme.Usage(percent)
	}
	mem := cell{text: stats.MemAmount()}
	if percent, ok := stats.MemPercent(); ok {
		mem.style = theme.Usage(percent)
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
		cpuText = theme.Usage(percent).Render(cpuText)
	}
	line := fmt.Sprintf(" %-*s  %s", nameWidth, service.Service, cpuText)
	if sparkWidth > 0 {
		line += "  " + liveStrip(series, sparkWidth)
	}
	line += fmt.Sprintf("  %*s", memWidth, stats.MemAmount())
	if peak := peakOf(series); peak > 0 {
		line += theme.Dim.Render(fmt.Sprintf("  peak %5.1f%%", peak))
	}
	return line
}

// liveStrip draws the newest width samples of a container's CPU, right-aligned
// so the right-hand end means "just now" on every row however much history
// each has.
//
// It is scaled against its own window and coloured by each reading, like
// every other strip (internal/tui/spark): height says how the reading moved,
// colour says how bad it is. The window is not clamped at a hundred, because
// a container's CPU is not a share of anything finite — two busy cores read
// as 200%, which is docker's convention and the table's.
func liveStrip(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	floor, ceiling := spark.Window(values, spark.MinimumSpan)
	return strings.Repeat(" ", width-len(values)) + spark.Strip(values, floor, ceiling,
		func(index int) lipgloss.Style { return theme.Usage(values[index]) })
}

func peakOf(values []float64) float64 {
	peak := 0.0
	for _, value := range values {
		peak = max(peak, value)
	}
	return peak
}

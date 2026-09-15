package status

import (
	"fmt"
	"strings"
	"time"

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
	layout := liveLayoutFor(rows, width)
	title := m.liveTitle(rows, layout)
	rule := theme.Dim.Render(" ──" + title + strings.Repeat("─", max(width-len(title)-4, 0)))
	if len(rows) == 0 {
		return rule + "\n" + theme.Dim.Render("  "+m.emptyLiveHint())
	}
	lines := make([]string, 0, len(rows))
	for _, service := range rows {
		lines = append(lines, m.renderLiveRow(service, layout))
	}
	return rule + "\n" + strings.Join(lines, "\n")
}

// The live row's fixed columns. The peak column is its text, "  peak 12.3%".
const (
	liveCPUWidth  = 7
	liveMemWidth  = 9
	livePeakWidth = 13
	// liveStripMin is the narrowest strip worth drawing; below it a row gives
	// its strips up and keeps its numbers, which are what the next sample
	// cannot reconstruct.
	liveStripMin = 8
	// liveStripMax caps a strip. It used to take every cell the numbers left,
	// right-aligned, so a strip holding forty samples on a wide terminal stood
	// eighty blank cells away from the number it belonged to.
	liveStripMax = 60
	// memoryShare is the narrowest window a memory strip is drawn against, as
	// a share of the largest reading: a container moving by a megabyte in
	// four hundred is flat, one that has put on forty is climbing.
	memoryShare = 0.1
)

// liveLayout is how wide the live rows' columns are, the same for every row
// so the strips start in one column.
type liveLayout struct {
	name, strip int
}

func liveLayoutFor(rows []compose.Service, width int) liveLayout {
	var layout liveLayout
	for _, service := range rows {
		layout.name = max(layout.name, len(service.Service))
	}
	fixed := 1 + layout.name + 2 + liveCPUWidth + 2 + liveMemWidth + livePeakWidth
	// Two strips, each behind a two-cell gap, and a cell of slack for a
	// memory reading one character wider than its column.
	layout.strip = min((width-fixed-5)/2, liveStripMax)
	if layout.strip < liveStripMin {
		layout.strip = 0
	}
	return layout
}

// liveTitle names the panel, its cadence, and how much time its strips
// cover. The span is measured from the samples drawn rather than from the
// cadence, since a strip that opened on the sampled source's history covers
// five seconds a cell on its left and one on its right.
func (m *Model) liveTitle(rows []compose.Service, layout liveLayout) string {
	if m.statsStarting {
		return " live · starting… "
	}
	seconds := 0.0
	if layout.strip > 0 {
		for _, service := range rows {
			_, covered := trendStrip(m.trends[service.Name], layout.strip, cpuReading, cpuScale)
			seconds = max(seconds, covered)
		}
	}
	if seconds >= 1 {
		return fmt.Sprintf(" live · 1s · %s ", spark.Span(seconds))
	}
	return " live · 1s "
}

func (m *Model) emptyLiveHint() string {
	if m.statsStarting {
		return "(waiting for the first sample…)"
	}
	return "(no running containers to sample)"
}

// renderLiveRow is a container's CPU with its strip beside it, then its
// memory with its own — each strip next to the number it is the history of.
func (m *Model) renderLiveRow(service compose.Service, layout liveLayout) string {
	stats := m.stats[service.Name]
	points := m.trends[service.Name]
	cpuText := fmt.Sprintf("%*s", liveCPUWidth, stats.CPUPerc)
	if percent, ok := stats.CPUPercent(); ok {
		cpuText = theme.Usage(percent).Render(cpuText)
	}
	line := fmt.Sprintf(" %-*s  %s", layout.name, service.Service, cpuText)
	if layout.strip > 0 {
		strip, _ := trendStrip(points, layout.strip, cpuReading, cpuScale)
		line += "  " + strip
	}
	line += fmt.Sprintf("  %*s", liveMemWidth, stats.MemAmount())
	if layout.strip > 0 {
		strip, _ := trendStrip(points, layout.strip, memoryReading, memoryScale)
		line += "  " + strip
	}
	if peak := peakCPU(points); peak > 0 {
		line += theme.Dim.Render(fmt.Sprintf("  peak %5.1f%%", peak))
	}
	return line
}

// A trendReading is what a strip draws from a point: the value whose shape is
// drawn, the percentage its colour is judged by, and whether the point has
// one at all.
type trendReading func(point) (value, percent float64, ok bool)

func cpuReading(p point) (float64, float64, bool) { return p.cpu, p.cpu, p.hasCPU }

func memoryReading(p point) (float64, float64, bool) {
	return p.memory, p.memoryPercent, p.hasMemory
}

// cpuScale is not clamped at a hundred: a container's CPU is not a share of
// anything finite, and two busy cores read as 200%.
func cpuScale(values []float64) (float64, float64) { return spark.Window(values, spark.MinimumSpan) }

// memoryScale is drawn in bytes rather than as a share of a limit. Most
// containers have no limit, their share is of the host's memory, and a leak
// of forty megabytes on a machine of thirty-two gigabytes would be a tenth of
// a point: invisible against any percentage window.
func memoryScale(values []float64) (float64, float64) { return spark.Relative(values, memoryShare) }

// trendStrip draws the newest width readings of a trend, left-aligned and
// padded out to width, and reports how many seconds they cover.
//
// Left-aligned so a strip starts beside the number it is the history of and
// grows away from it, with every column after it staying where it is. Aligned
// to the right, as the system view's column is, a strip still filling up
// stood as many blank cells from its number as it had yet to fill — and the
// rows of this panel all begin together, so their right-hand ends mean "just
// now" together as well, except for a container that has only just arrived.
//
// Height says how the reading moved and colour says how bad it is, the rule
// every strip follows (internal/tui/spark): each cell is coloured by its own
// percentage, so a climbing memory strip stays green until the reading
// itself is worth a colour.
func trendStrip(points []point, width int, read trendReading, scale func([]float64) (float64, float64)) (string, float64) {
	if width <= 0 {
		return "", 0
	}
	var values, percents []float64
	var first, last time.Time
	for _, p := range points {
		value, percent, ok := read(p)
		if !ok {
			continue
		}
		values, percents = append(values, value), append(percents, percent)
		if first.IsZero() {
			first = p.at
		}
		last = p.at
	}
	if len(values) > width {
		// The first drawn point is the one width from the end.
		drop := len(values) - width
		kept := 0
		for _, p := range points {
			if _, _, ok := read(p); ok {
				if kept == drop {
					first = p.at
					break
				}
				kept++
			}
		}
		values, percents = values[drop:], percents[drop:]
	}
	floor, ceiling := scale(values)
	strip := spark.Strip(values, floor, ceiling,
		func(index int) lipgloss.Style { return theme.Usage(percents[index]) }) +
		strings.Repeat(" ", width-len(values))
	if len(values) < 2 {
		return strip, 0
	}
	return strip, last.Sub(first).Seconds()
}

func peakCPU(points []point) float64 {
	peak := 0.0
	for _, p := range points {
		if p.hasCPU {
			peak = max(peak, p.cpu)
		}
	}
	return peak
}

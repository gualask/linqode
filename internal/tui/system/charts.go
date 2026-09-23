package system

// The machine's recent history, drawn as a row of short charts.
//
// It used to be a column of strips one row high beside the readings. One row
// is eight heights to draw a history with, the column vanished altogether
// wherever the widest reading left fewer than twenty-four cells, and the
// screen under the process list stayed empty. The charts take that room
// instead: a row of them under the readings, each as wide as its share of
// the terminal.
//
// They were tall for a while, on the argument that height buys resolution: a
// share drawn from nought to a hundred over ten rows is eighty heights, and
// memory going from 78% to 88% is eight of them. What that bought in
// practice was a wall — twelve rows where a busy machine is a solid block of
// colour and an idle one is empty air, over a process list that had been
// squeezed to make room for it. The chart is read for *when* something
// happened, not for how much; the meter above it already says how much. So
// the height is fixed and small now, and the rows go to the processes.
//
// The columns are drawn from the first frame, filled or not. The charts used
// to appear a sample after the view opened — a trend needs two readings —
// and pushed the process list down the screen as they did; a chart that has
// its shape before it has its history simply fills up.
//
// What is drawn in those rows is solid columns over a track — the meter's
// shape stood on end — because at four rows the finer drawing is a ragged
// edge of eighths. They were two cells wide with a cell of air between them
// for an afternoon, which read as a grid rather than as a history: the
// columns touch now, a sample each, and the air is gone.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

// chart is one reading's history: what it is called, what it reads now, how
// to take it from a sample, what the top of the chart stands for, and the
// colour a value is judged by.
type chart struct {
	label   string
	reading string
	of      func(trend) float64
	ceiling func(values []float64) float64
	style   func(value float64) lipgloss.Style
	// note is said beside the reading, dimmed: for a rate, the peak the
	// chart is scaled to, since its top is not a number anybody knows.
	note func(values []float64) string
}

// unknownReading stands where a number needs two samples and only one has
// arrived: the CPU share, the per-core strip, both throughputs. It is said in
// the readings and in the charts, which is why it is spelled in one place.
const unknownReading = "—"

const (
	// chartMinWidth is the narrowest a chart is drawn: under it the four
	// wrap into two rows, then into one column.
	chartMinWidth = 36
	chartGap      = 3
	// chartMinHeight is a title and three rows of history.
	chartMinHeight = 4
	// chartMaxHeight is a title and four rows of history, which is eight
	// heights for a bar. A fifth row is a height nobody reads and a row the
	// processes can use.
	chartMaxHeight = 5
	// chartDefaultHeight is used before the view has been told its size.
	chartDefaultHeight = chartMaxHeight
)

// charts are the readings worth a history, once there are two samples to
// draw one from.
func (m *Model) charts() []chart {
	if !m.loaded {
		return nil
	}
	share := func([]float64) float64 { return 100 }
	peak := func(values []float64) float64 {
		_, high := spark.Rate(values)
		return high
	}
	peakNote := func(values []float64) string {
		if high := peak(values); high > 0 {
			return "peak " + formatRate(high)
		}
		return ""
	}
	// A throughput is not a share of anything, so it is drawn in one colour:
	// colouring it by height would read as "this is bad" where it only means
	// "this is the top of what happened".
	cyan := func(float64) lipgloss.Style { return theme.Cyan }
	// A difference needs two samples, and the charts are on screen before
	// the second one arrives. The reading says so rather than saying zero.
	unknown := theme.Dim.Render(unknownReading)

	cpu := unknown
	if m.hasUsage {
		busy := m.usage.CPUPercent
		cpu = theme.Usage(busy).Render(fmt.Sprintf("%.0f%% busy", busy))
	}
	list := []chart{{
		label: "cpu", of: func(t trend) float64 { return t.cpu },
		reading: cpu, ceiling: share, style: theme.Usage,
	}}
	if m.metrics.MemTotalKB > 0 {
		memory := m.metrics.MemUsedPercent()
		list = append(list, chart{
			label: "memory", of: func(t trend) float64 { return t.memory },
			reading: theme.Usage(memory).Render(fmt.Sprintf("%.0f%% used", memory)),
			ceiling: share, style: theme.Usage,
		})
	}
	// Which charts there are is read off the sample rather than off the
	// rates, which arrive one sample later: a row of charts that grows a
	// column once the second sample lands is the jump this is avoiding.
	if len(m.metrics.Interfaces) > 0 {
		down, up := unknown, unknown
		if m.hasUsage {
			down, up = formatRate(m.usage.RxRate), formatRate(m.usage.TxRate)
		}
		list = append(list,
			chart{label: "net down", of: func(t trend) float64 { return t.rx },
				reading: down, ceiling: peak, style: cyan, note: peakNote},
			chart{label: "net up", of: func(t trend) float64 { return t.tx },
				reading: up, ceiling: peak, style: cyan, note: peakNote})
	}
	return list
}

// chartsPerRow is how many charts share a row: all of them where each gets
// chartMinWidth, otherwise two, otherwise one. Never three of four, which
// reads as a fourth that did not fit rather than as a grid.
func chartsPerRow(width, count int) int {
	if count == 0 {
		return 0
	}
	if width <= 0 {
		return count
	}
	fits := max((width+chartGap)/(chartMinWidth+chartGap), 1)
	switch {
	case fits >= count:
		return count
	case fits >= 2:
		return 2
	default:
		return 1
	}
}

// room divides what the readings leave between the charts and the processes,
// and is the one place that decides what gives way. Each block is preceded by
// a blank row.
//
// There is nothing to negotiate any more: a chart is five rows, or four where
// that is what fits, and everything past them is the processes'. The charts
// used to take a share of the room and grow into whatever the process list
// left empty, which is how a screen of four charts twelve rows tall over
// nine processes happened.
//
// When only one of the two fits, it is the processes: the numbers before the
// shapes, because the next sample reconstructs a chart and nothing
// reconstructs which process was on top.
func (m *Model) room(available int) (chartHeight, processRows int) {
	charts := m.charts()
	perRow := chartsPerRow(m.width, len(charts))
	gridRows := 0
	if perRow > 0 {
		gridRows = (len(charts) + perRow - 1) / perRow
	}
	if m.height <= 0 {
		// Before the view knows its size there is no room to divide: the
		// charts are drawn at a size that shows them, and the processes wait.
		if gridRows > 0 {
			return chartDefaultHeight, 0
		}
		return 0, 0
	}
	processes := len(m.processes) > 0
	chartsFit := gridRows > 0 && available-1 >= gridRows*chartMinHeight
	switch {
	case chartsFit && processes && available-2 >= gridRows*chartMinHeight+processMinRows:
		height := chartMaxHeight
		if available-2-gridRows*height < processMinRows {
			height = chartMinHeight
		}
		return height, available - 2 - gridRows*height
	case processes && available-1 >= processMinRows:
		return 0, available - 1
	case chartsFit:
		return min((available-1)/gridRows, chartMaxHeight), 0
	}
	return 0, 0
}

// chartLines draws every chart at height, perRow of them to a row.
func (m *Model) chartLines(height int) []string {
	charts := m.charts()
	perRow := chartsPerRow(m.width, len(charts))
	if perRow == 0 {
		return nil
	}
	width := m.width
	if width <= 0 {
		width = perRow*(chartMinWidth+chartGap) - chartGap
	}
	chartWidth := (width - (perRow-1)*chartGap) / perRow
	var lines []string
	for start := 0; start < len(charts); start += perRow {
		row := charts[start:min(start+perRow, len(charts))]
		drawn := make([][]string, len(row))
		for index, c := range row {
			drawn[index] = m.drawChart(c, chartWidth, height)
		}
		for line := range height {
			var b strings.Builder
			for index := range row {
				cell := drawn[index][line]
				b.WriteString(cell)
				if index < len(row)-1 {
					b.WriteString(strings.Repeat(" ",
						max(chartWidth-lipgloss.Width(cell), 0)+chartGap))
				}
			}
			lines = append(lines, b.String())
		}
	}
	return lines
}

// drawChart is one chart in exactly height lines: its title, with the reading
// now and how far back the chart reaches, and the history under it as solid
// bars with the newest against the right edge.
func (m *Model) drawChart(c chart, width, height int) []string {
	// A cell of margin on the left, where every reading row starts too.
	inner := max(width-1, 1)
	samples, seconds := m.history.window(inner)
	values := make([]float64, len(samples))
	for index, sample := range samples {
		values[index] = c.of(sample)
	}
	lines := []string{m.chartTitle(c, values, seconds, width)}

	// Every column is drawn, from the first frame on, and the readings fill
	// them from the right. Drawing only the columns there were meant a chart
	// that appeared out of nothing a sample after the view opened and pushed
	// the processes down the screen; an empty column says what the empty half
	// of a meter says.
	columns := make([]float64, inner)
	copy(columns[inner-len(values):], values)
	for _, bar := range spark.Bars(columns, c.ceiling(values), height-1,
		func(index int) lipgloss.Style { return c.style(columns[index]) }, theme.Track) {
		lines = append(lines, " "+bar)
	}
	return lines
}

// chartTitle is the chart's name, what it reads now, and at the right end how
// far back it reaches — which was a row of its own, an axis under the
// history, until the charts got short enough for that row to be a fifth of
// them. A title with no room for the span is drawn without it: the span is
// the part a narrow chart can best spare, since every chart on the screen
// covers the same minutes.
func (m *Model) chartTitle(c chart, values []float64, seconds float64, width int) string {
	title := " " + theme.Bold.Render(c.label) + "  " + c.reading
	if c.note != nil {
		if note := c.note(values); note != "" {
			title += theme.Dim.Render("   " + note)
		}
	}
	if seconds <= 0 {
		return fit(title, width)
	}
	span := theme.Dim.Render(spark.Span(seconds))
	if gap := width - lipgloss.Width(title) - lipgloss.Width(span) - 1; gap >= 2 {
		return title + strings.Repeat(" ", gap) + span + " "
	}
	return fit(title, width)
}

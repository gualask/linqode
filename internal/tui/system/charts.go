package system

// The machine's recent history, drawn tall.
//
// It used to be a column of strips one row high beside the readings. One row
// is eight heights to draw a history with, the column vanished altogether
// wherever the widest reading left fewer than twenty-four cells, and the
// screen under the process list stayed empty. The charts take that room
// instead: a row of them under the readings, as tall as the readings leave,
// each as wide as its share of the terminal.
//
// The height is what lets a chart be drawn against the reading's own scale —
// nought to a hundred for a share, nought to the busiest moment for a rate.
// A one-row strip had to be scaled against its own window to show a climb at
// all; ten rows are eighty heights, and memory going from 78% to 88% is eight
// of them. So the height is the level, as it is on the meter above, and the
// colour is how bad it is.

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

const (
	// chartMinWidth is the narrowest a chart is drawn: under it the four
	// wrap into two rows, then into one column.
	chartMinWidth = 36
	chartGap      = 3
	// chartMinHeight is a title, three rows of history, and the axis.
	chartMinHeight = 5
	// chartMaxHeight caps a chart at twelve rows of history, ninety-six
	// heights. Past that a chart adds resolution nobody reads and takes rows
	// the processes can use.
	chartMaxHeight = 14
	// chartShare is how much of the room under the readings the charts are
	// given when the processes want it too, as a percentage.
	chartShare = 45
	// chartDefaultHeight is used before the view has been told its size.
	chartDefaultHeight = 8
)

// charts are the readings worth a history, once there are two samples to
// draw one from.
func (m *Model) charts() []chart {
	if !m.hasUsage {
		return nil
	}
	if samples, _ := m.history.window(2); samples == nil {
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

	cpu := m.usage.CPUPercent
	list := []chart{{
		label: "cpu", of: func(t trend) float64 { return t.cpu },
		reading: theme.Usage(cpu).Render(fmt.Sprintf("%.0f%% busy", cpu)),
		ceiling: share, style: theme.Usage,
	}}
	if m.metrics.MemTotalKB > 0 {
		memory := m.metrics.MemUsedPercent()
		list = append(list, chart{
			label: "memory", of: func(t trend) float64 { return t.memory },
			reading: theme.Usage(memory).Render(fmt.Sprintf("%.0f%% used", memory)),
			ceiling: share, style: theme.Usage,
		})
	}
	if len(m.usage.Interfaces) > 0 {
		list = append(list,
			chart{label: "net down", of: func(t trend) float64 { return t.rx },
				reading: formatRate(m.usage.RxRate), ceiling: peak, style: cyan, note: peakNote},
			chart{label: "net up", of: func(t trend) float64 { return t.tx },
				reading: formatRate(m.usage.TxRate), ceiling: peak, style: cyan, note: peakNote})
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
// Both, when both fit at their least: the charts take chartShare of the room,
// within their bounds, and the processes the rest — and rows the processes
// cannot fill go back to the charts. One, when only one fits, and then it is
// the processes: the numbers before the shapes, because the next sample
// reconstructs a chart and nothing reconstructs which process was on top.
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
		total := min(max((available-2)*chartShare/100, gridRows*chartMinHeight),
			gridRows*chartMaxHeight)
		chartHeight = total / gridRows
		processRows = available - 2 - chartHeight*gridRows
		needed := blockHeadings + len(m.processes)
		if m.processesStale {
			needed++
		}
		if surplus := processRows - needed; surplus > 0 {
			grow := min(surplus/gridRows, chartMaxHeight-chartHeight)
			chartHeight += grow
			processRows -= grow * gridRows
		}
		return chartHeight, processRows
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

// drawChart is one chart in exactly height lines: its title with the reading
// now, the history a sample a cell with the newest on the right, and an axis
// saying how far back the drawn samples reach.
func (m *Model) drawChart(c chart, width, height int) []string {
	// A cell of margin on the left, where every reading row starts too.
	inner := max(width-1, 1)
	samples, seconds := m.history.window(inner)
	values := make([]float64, len(samples))
	for index, sample := range samples {
		values[index] = c.of(sample)
	}
	title := " " + theme.Bold.Render(c.label) + "  " + c.reading
	if c.note != nil {
		if note := c.note(values); note != "" {
			title += theme.Dim.Render("   " + note)
		}
	}
	lines := []string{fit(title, width)}

	pad := strings.Repeat(" ", inner-len(values))
	bars := spark.Columns(values, c.ceiling(values), height-2, func(index int) lipgloss.Style {
		return c.style(values[index])
	})
	for _, bar := range bars {
		lines = append(lines, " "+pad+bar)
	}

	// The axis starts under the oldest sample drawn, so "-4m" sits where
	// four minutes ago actually is rather than at the left edge of a chart
	// that has not filled yet.
	left := "-" + spark.Span(seconds)
	axis := strings.Repeat(" ", inner-3) + "now"
	if gap := len(values) - len(left) - len("now"); gap >= 1 {
		axis = pad + left + strings.Repeat(" ", gap) + "now"
	}
	return append(lines, theme.Dim.Render(" "+axis))
}

package system

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

// Four charts share a row where each gets its minimum width, wrap into two
// rows of two where they do not, and never into three over one.
func TestChartsWrapIntoAGrid(t *testing.T) {
	cases := []struct{ width, count, want int }{
		{160, 4, 4}, {140, 4, 2}, {100, 4, 2}, {60, 4, 1}, {0, 4, 4}, {100, 2, 2}, {100, 0, 0},
	}
	for _, c := range cases {
		if got := chartsPerRow(c.width, c.count); got != c.want {
			t.Errorf("chartsPerRow(%d, %d) = %d, want %d", c.width, c.count, got, c.want)
		}
	}
}

// busyMachine is a model with a history, charts, and a process table, at the
// given size.
func busyMachine(width, height int) *Model {
	m := climbing(40)
	m.SetOpen(true)
	m.SetSize(width, height)
	m.SetProcesses(running(), nil)
	after := running()
	after.UptimeSeconds += 10
	m.SetProcesses(after, nil)
	return m
}

// The charts are drawn from the first sample, before there is a trend to put
// in them. They used to arrive a sample later and push the process list down
// the screen as they did.
func TestTheChartSlotsAreDrawnBeforeTheFirstTrend(t *testing.T) {
	m := New("", "")
	m.SetSample(richMetrics(), nil)
	m.SetOpen(true)
	m.SetSize(160, 40)
	charts := m.charts()
	if len(charts) == 0 {
		t.Fatal("one sample drew no charts")
	}
	height, _ := m.room(40 - len(m.readingLines()))
	if height != chartMaxHeight {
		t.Errorf("the charts were given %d rows before the first trend, want %d",
			height, chartMaxHeight)
	}
	lines := m.drawChart(charts[0], 40, chartMaxHeight)
	if len(lines) != chartMaxHeight {
		t.Fatalf("a chart with no history drew %d lines", len(lines))
	}
	// A reading that needs two samples says so rather than saying nought.
	if !strings.Contains(lines[0], "—") {
		t.Errorf("the cpu title claims a reading it cannot have yet: %q", lines[0])
	}
	if strings.ContainsAny(strings.Join(lines[1:], ""), spark.Glyphs) {
		t.Errorf("a chart with no history drew a bar:\n%s", strings.Join(lines, "\n"))
	}
}

// The charts take their rows and the processes take the rest. A chart is a
// fixed few rows now, so a short list of processes leaves the screen empty
// rather than growing the charts into it.
func TestTheChartsTakeTheirRowsAndTheProcessesTakeTheRest(t *testing.T) {
	m := busyMachine(160, 60)
	charts, processes := m.room(40)
	if charts != chartMaxHeight {
		t.Errorf("charts given %d rows, want %d", charts, chartMaxHeight)
	}
	if want := 40 - 2 - chartMaxHeight; processes != want {
		t.Errorf("processes given %d of the remaining rows, want %d", processes, want)
	}
	// Room enough for the charts but barely enough for the lists: the charts
	// give up their fifth row before the processes give up a process.
	charts, processes = m.room(2 + chartMinHeight + processMinRows)
	if charts != chartMinHeight || processes != processMinRows {
		t.Errorf("a tight view gave charts %d and processes %d, want %d and %d",
			charts, processes, chartMinHeight, processMinRows)
	}
}

// On a short terminal the charts give way first: the next sample
// reconstructs a chart, and nothing reconstructs which process was on top.
func TestAShortViewKeepsTheProcessesBeforeTheCharts(t *testing.T) {
	m := busyMachine(160, 60)
	charts, processes := m.room(9)
	if charts != 0 || processes != 8 {
		t.Errorf("nine rows gave charts %d and processes %d, want 0 and 8", charts, processes)
	}
	if charts, processes := m.room(3); charts != 0 || processes != 0 {
		t.Errorf("three rows gave charts %d and processes %d, want nothing", charts, processes)
	}
}

// The view is no taller than it was given, whatever the height, and every
// block in it is one it had the room for. Below the height of the readings
// themselves it is the panel's box that cuts them, from the bottom, as it
// always has: the charts and the processes are the view's to fit.
func TestTheViewFitsItsHeight(t *testing.T) {
	for _, width := range []int{100, 160, 240} {
		for height := 12; height <= 60; height += 4 {
			m := busyMachine(width, height)
			view := m.View()
			if got := strings.Count(view, "\n") + 1; got > height {
				t.Errorf("at %dx%d the view drew %d lines", width, height, got)
			}
			for index, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("at %dx%d line %d is %d wide: %q", width, height, index, w, line)
				}
			}
		}
	}
}

// A share is drawn against nought to a hundred: at 10% busy a chart has its
// bar in the bottom row and nothing above it. The title carries how far back
// the chart reaches, where an axis row used to.
func TestShareChartsAreDrawnAgainstTheirWholeScale(t *testing.T) {
	m := New("", "")
	m.history.push(trend{uptime: 1, cpu: 10})
	m.history.push(trend{uptime: 6, cpu: 10})
	c := chart{label: "cpu", of: func(t trend) float64 { return t.cpu },
		ceiling: func([]float64) float64 { return 100 },
		style:   func(float64) lipgloss.Style { return theme.Green }}
	lines := m.drawChart(c, 40, chartMaxHeight)
	if len(lines) != chartMaxHeight {
		t.Fatalf("a chart of height %d drew %d lines", chartMaxHeight, len(lines))
	}
	for row := 1; row < chartMaxHeight-1; row++ {
		if strings.ContainsAny(lines[row], spark.Glyphs) {
			t.Errorf("10%% reached row %d of %d:\n%s", row, chartMaxHeight-1,
				strings.Join(lines, "\n"))
		}
	}
	if !strings.ContainsAny(lines[chartMaxHeight-1], spark.Glyphs) {
		t.Errorf("10%% drew nothing in the bottom row:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "5s") {
		t.Errorf("the title does not say how far back the chart reaches: %q", lines[0])
	}
}

// The readings are flush right, so the newest column is always in the same
// place and a chart that has not filled yet fills from the right.
func TestTheNewestColumnIsAgainstTheRightEdge(t *testing.T) {
	m := busyMachine(160, 60)
	bottom := m.drawChart(m.charts()[0], 40, chartMaxHeight)[chartMaxHeight-1]
	if !strings.HasSuffix(bottom, "█") && !strings.HasSuffix(bottom, "▄") {
		t.Errorf("the bottom row does not end in a column: %q", bottom)
	}
}

// Every reading worth a history has a chart, and a rate's chart says the
// peak it is scaled to.
func TestTheChartsAreDrawn(t *testing.T) {
	view := busyMachine(160, 60).View()
	for _, want := range []string{"cpu  ", "memory  ", "net down", "net up", "peak ", "3m"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view is missing %q:\n%s", want, view)
		}
	}
}

// The readings keep their rows from the first sample on. Three of them are
// differences between two samples — the CPU share, the per-core strip, both
// throughputs — and the rows that carried them used to arrive one sample
// late, pushing the charts and the process lists down the screen.
func TestTheReadingsKeepTheirRowsBeforeTheFirstDifference(t *testing.T) {
	first, second := climbing(1), climbing(2)
	for _, m := range []*Model{first, second} {
		m.SetOpen(true)
		m.SetSize(160, 40)
	}
	before, after := first.readingLines(), second.readingLines()
	if len(before) != len(after) {
		t.Errorf("the readings are %d rows on the first sample and %d on the second:\n%s\n---\n%s",
			len(before), len(after), strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
	if !strings.Contains(strings.Join(before, "\n"), unknownReading) {
		t.Errorf("the first sample claims readings it cannot have:\n%s",
			strings.Join(before, "\n"))
	}
}

// Where the terminal has room for two columns the readings take them: what
// fills up on the left, what the machine is doing on the right.
func TestReadingsTakeTwoColumnsWhenWide(t *testing.T) {
	side := func(width int) bool {
		m := measured()
		m.SetSize(width, 40)
		for _, line := range strings.Split(m.View(), "\n") {
			if strings.Contains(line, "busy   load") && strings.Contains(line, "down") {
				return true
			}
		}
		return false
	}
	if !side(220) {
		t.Error("a 220-column view kept the readings in one column")
	}
	if side(150) {
		t.Error("a 150-column view put the readings in two columns")
	}
}

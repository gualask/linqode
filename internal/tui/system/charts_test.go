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

// The room under the readings is shared: the charts take their share within
// their bounds, the processes the rest, and rows the processes cannot fill go
// back to the charts.
func TestRoomIsSharedBetweenChartsAndProcesses(t *testing.T) {
	m := busyMachine(160, 60)
	charts, processes := m.room(40)
	if charts < chartMinHeight || charts > chartMaxHeight {
		t.Errorf("charts given %d rows, outside %d–%d", charts, chartMinHeight, chartMaxHeight)
	}
	if processes < processMinRows {
		t.Errorf("processes given %d rows, fewer than %d", processes, processMinRows)
	}
	if used := 2 + charts + processes; used > 40 {
		t.Errorf("the blocks take %d rows of 40", used)
	}
	// Four processes need six rows; everything past that is the charts'.
	if charts != chartMaxHeight {
		t.Errorf("charts given %d rows while the processes left rows empty", charts)
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

// A share is drawn against nought to a hundred: at 10% busy a chart with
// eight rows of history has its bars in the bottom row and nothing above.
func TestShareChartsAreDrawnAgainstTheirWholeScale(t *testing.T) {
	m := New("", "")
	m.history.push(trend{uptime: 1, cpu: 10})
	m.history.push(trend{uptime: 6, cpu: 10})
	c := chart{label: "cpu", of: func(t trend) float64 { return t.cpu },
		ceiling: func([]float64) float64 { return 100 },
		style:   func(float64) lipgloss.Style { return theme.Green }}
	lines := m.drawChart(c, 40, 10)
	if len(lines) != 10 {
		t.Fatalf("a chart of height 10 drew %d lines", len(lines))
	}
	for row := 1; row < 8; row++ {
		if strings.ContainsAny(lines[row], spark.Glyphs) {
			t.Errorf("10%% reached row %d of 8:\n%s", row, strings.Join(lines, "\n"))
		}
	}
	if !strings.ContainsAny(lines[8], spark.Glyphs) {
		t.Errorf("10%% drew nothing in the bottom row:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[9], "now") {
		t.Errorf("the axis does not end at now: %q", lines[9])
	}
}

// Every reading worth a history has a chart, and a rate's chart says the
// peak it is scaled to.
func TestTheChartsAreDrawn(t *testing.T) {
	view := busyMachine(160, 60).View()
	for _, want := range []string{"cpu  ", "memory  ", "net down", "net up", "peak ", "now"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view is missing %q:\n%s", want, view)
		}
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

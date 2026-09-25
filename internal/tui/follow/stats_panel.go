package follow

// The stats panel beside the log: what it counts, how it is laid out, and
// which of its rows the cursor may stand on to pick a filter.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

// statsDrawn reports whether the stats panel is on screen: open, and on a
// terminal wide enough to leave the log some room beside it.
func (m *Model) statsDrawn() bool {
	return m.showStats && m.width > statsWidth+20
}

// statsKeys reports whether the keys go to the stats panel. Having been handed
// them is not enough: a panel the terminal is too narrow to draw would take
// the arrows and enter for a cursor nobody can see, and change the filter
// from a row nobody read. The keys come back to it, focus kept, when the
// terminal is widened.
func (m *Model) statsKeys() bool {
	return m.statsFocus && m.statsDrawn()
}

// statsView is the side panel: how many lines, and how they divide between
// levels and between the values of a field. Its rows are what the filter is
// picked from: a dot marks the ones the filter holds, and with the panel
// focused the cursor is drawn on the row enter would pick.
//
// It never draws more rows than the log beside it has. It used to draw as
// many as it had counts, and on a short terminal that pushed the footer off
// the bottom of the screen; now it is cut from the bottom, where the least
// frequent values are.
func (m *Model) statsView() string {
	width := statsWidth - 1 // the panel's left padding
	stats, levels, values := m.statsRows()
	window := formatWindow(stats.Recent)
	clock := "by arrival"
	if stats.Clock == logs.ByLogTime {
		clock = "by log time"
	}
	lines := []string{
		heldLines(stats),
		theme.Dim.Render(fmt.Sprintf("last %s · %s", window, clock)),
		"",
	}
	cursor := -1
	if m.statsKeys() {
		cursor = m.cursorIndex(m.drawnPicks(levels, values))
	}
	mark := func(field string, offset int) func(index int, key string) (bool, bool) {
		return func(index int, key string) (bool, bool) {
			return m.store.Filter().Has(field, key), index+offset == cursor
		}
	}
	rows := countList(width, "levels", "(none)", window, levels, levelStyle, mark(logs.LevelKey, 0))
	if len(levels) > 0 {
		rows = slices.Insert(rows, 1, levelBar(width, levels))
	}
	lines = append(lines, rows...)
	lines = append(lines, "")
	if m.topField == "" {
		lines = append(lines, theme.Dim.Render("(no fields to count by)"))
	} else {
		lines = append(lines, countList(width, "top "+terminalText(m.topField),
			"(no values)", window, values, plainLabel, mark(m.topField, len(levels)))...)
	}
	if m.viewport > 0 && len(lines) > m.viewport {
		lines = lines[:m.viewport]
	}
	return strings.Join(lines, "\n")
}

// pick is one row of the stats panel as a filter term: a field and a value.
type pick struct{ field, value string }

// statsRows is the counts the panel draws, in the order it draws them: the
// levels worst first, in the bar and in the rows under it, so the two are
// read in the same direction; the field's values by how many, which is the
// only order they have, cut to topValues.
//
// The field is chosen here the first time there is one to choose: the
// panel is useful before anyone has told it which field matters, and once
// chosen it stays, so a field that overtakes it as lines arrive does not
// swap the list out from under the cursor.
func (m *Model) statsRows() (logs.Stats, []logs.Count, []logs.Count) {
	if m.topField == "" {
		if fields := m.store.Fields(); len(fields) > 0 {
			m.topField = fields[0]
		}
	}
	stats := m.store.ComputeStats(m.topField, m.now())
	values := stats.Values
	if len(values) > topValues {
		values = values[:topValues]
	}
	return stats, worstFirst(stats.Levels), values
}

// picks is every row the cursor can stand on, top to bottom.
func (m *Model) picks() []pick {
	_, levels, values := m.statsRows()
	return m.drawnPicks(levels, values)
}

// statsHead is the panel's rows above the levels: the lines held, the window,
// and a blank.
const statsHead = 3

// pickRows is the panel row each pick is drawn on, in the order picksOf
// lists them: under the levels' heading and their bar, then under a blank and
// the field's heading. It is statsView's layout written as arithmetic, and a
// test holds the two together.
func pickRows(levels, values int) []int {
	rows := make([]int, 0, levels+values)
	first := statsHead + 1 // the heading
	if levels > 0 {
		first++ // the bar
	}
	for index := range levels {
		rows = append(rows, first+index)
	}
	first += max(levels, 1) + 2 // the rows or "(none)", a blank, the heading
	for index := range values {
		rows = append(rows, first+index)
	}
	return rows
}

// drawnPicks is the picks the panel has room to draw. It is cut to the height
// of the log beside it, from the bottom, and a row that is not drawn is not
// one the cursor may stand on: enter would put in the filter a value the
// operator never saw.
func (m *Model) drawnPicks(levels, values []logs.Count) []pick {
	picks := m.picksOf(levels, values)
	if m.viewport <= 0 {
		return picks
	}
	for index, row := range pickRows(len(levels), len(values)) {
		if row >= m.viewport {
			return picks[:index]
		}
	}
	return picks
}

func (m *Model) picksOf(levels, values []logs.Count) []pick {
	picks := make([]pick, 0, len(levels)+len(values))
	for _, level := range levels {
		picks = append(picks, pick{logs.LevelKey, level.Key})
	}
	for _, value := range values {
		picks = append(picks, pick{m.topField, value.Key})
	}
	return picks
}

// cursorIndex is the row the cursor is on: the one it named, wherever that
// has moved to, or the position it had when that row is gone.
func (m *Model) cursorIndex(picks []pick) int {
	if index := slices.Index(picks, m.picked); index >= 0 {
		return index
	}
	return min(max(m.cursor, 0), max(len(picks)-1, 0))
}

// heldLines is what the counts under it are of: the lines in view, and the
// tail they were drawn from when a filter is hiding some of it.
func heldLines(stats logs.Stats) string {
	if stats.Lines != stats.Tail {
		return fmt.Sprintf("%d of %d lines", stats.Lines, stats.Tail)
	}
	return fmt.Sprintf("%d lines · %s", stats.Lines,
		theme.Magenta.Render(fmt.Sprintf("%d json", stats.Parsed)))
}

// levelBar is every level in view drawn as one bar of the panel's width, a
// segment each, in the order given — which is worst first, so the red starts
// at the left edge.
//
// It answers the question the counts cannot answer on their own: how big the
// problem is. Five errors is a sliver of a busy service and the whole of a
// quiet one, and "5" is the same number in both — while a bar that has gone
// red says which one you are looking at before you have read anything.
//
// What the bar is read for is whether there is any red at all, and a mark
// you have to hunt for along a row is a mark you will miss.
func levelBar(width int, levels []logs.Count) string {
	shares := make([]float64, len(levels))
	for index, count := range levels {
		shares[index] = float64(count.N)
	}
	return spark.Segments(shares, width, func(index int) lipgloss.Style {
		return levelStyle(levels[index].Key)
	}, theme.Track)
}

// worstFirst orders levels by severity, keeping the order the list came in
// within each of them — which is by how many, since that is how the counts
// beside the bar are sorted.
func worstFirst(levels []logs.Count) []logs.Count {
	ordered := slices.Clone(levels)
	slices.SortStableFunc(ordered, func(a, b logs.Count) int {
		return int(logs.SeverityOf(b.Key)) - int(logs.SeverityOf(a.Key))
	})
	return ordered
}

// countColumn is how wide each of a row's two numbers is drawn. Six cells
// hold the tail's whole capacity with a space in front of it, so the columns
// never touch and never move.
const countColumn = 6

// countList is one heading and the counts under it: what each key holds
// recently, and what it holds in all.
//
// Two numbers rather than a number and a bar. The question the panel is
// opened with is how many there are now, and that is a number or it is
// nothing. mark says, per row, whether the filter holds it — drawn as a dot
// in the row's first cell — and whether the cursor is on it.
func countList(width int, heading, empty, window string, counts []logs.Count,
	style func(key string) lipgloss.Style, mark func(index int, key string) (active, cursor bool)) []string {
	label := max(width-2*countColumn, 4)
	lines := []string{theme.Bold.Render(fmt.Sprintf("%-*s", label, heading)) +
		theme.Dim.Render(fmt.Sprintf("%*s%*s", countColumn, window, countColumn, "all"))}
	if len(counts) == 0 {
		return append(lines, theme.Dim.Render(" "+empty))
	}
	for index, count := range counts {
		active, cursor := mark(index, count.Key)
		dot := " "
		if active {
			dot = theme.Cyan.Render("•")
		}
		name := ansi.Truncate(terminalText(count.Key), label-1, "…")
		row := style(count.Key).Render(fmt.Sprintf("%-*s", label-1, name)) +
			fmt.Sprintf("%*d%*d", countColumn, count.Recent, countColumn, count.N)
		if cursor {
			row = theme.Reverse.Render(ansi.Strip(row))
		}
		lines = append(lines, dot+row)
	}
	return lines
}

// plainLabel is the styling a field's values get: none. A level is coloured
// because the colour is the reading — an error row is red wherever it
// appears — and a route is not one of those.
func plainLabel(string) lipgloss.Style { return lipgloss.NewStyle() }

// formatWindow writes the stretch the recent counts cover the shortest way:
// 5m, 6h, 1d.
func formatWindow(window time.Duration) string {
	switch {
	case window%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(window/(24*time.Hour)))
	case window%time.Hour == 0:
		return fmt.Sprintf("%dh", int(window/time.Hour))
	default:
		return fmt.Sprintf("%dm", int(window/time.Minute))
	}
}

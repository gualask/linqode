package follow

// One log line on screen: laid out as a record or as text, cut to its width
// in cells, and painted with the cursor and the search's hits.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

func levelStyle(level string) lipgloss.Style {
	// The two severities are the log engine's, so the level a line is painted
	// in and the colour its slice of the timeline takes cannot disagree.
	switch logs.SeverityOf(level) {
	case logs.SeverityError:
		return theme.Red
	case logs.SeverityWarning:
		return theme.Yellow
	}
	switch strings.ToLower(level) {
	case "info":
		return theme.Green
	case "debug":
		return theme.Blue
	case "trace":
		return theme.Dim
	default:
		return lipgloss.NewStyle()
	}
}

// lineMarks is what a log line carries beyond its text: the search hits,
// whether it holds the current one, and the cursor's fill when it is the
// selected line.
type lineMarks struct {
	query string
	// current: the first hit on this line is the current match — n/N step
	// from line to line, so that is the hit they stopped on.
	current bool
	// fill is the cursor's style on the selected line, nil elsewhere.
	fill *lipgloss.Style
}

// selectionStyle marks the log's cursor line: a lit bar while the log has
// the keys, a quiet fill while the stats panel does.
func (m *Model) selectionStyle() lipgloss.Style {
	if m.statsKeys() {
		return theme.SelectedIdle
	}
	return theme.Reverse
}

// searchText is a line as the log draws it, uncut: what the search looks in,
// so that a line it stops on shows the hit. A record's raw JSON holds its
// keys, its quotes and its escapes, none of which the structured form draws.
func (m *Model) searchText(line logs.LogLine) string {
	if m.structuredRendering() && line.Record != nil {
		if segments := structuredSegments(line.Record, 1<<20); len(segments) > 0 {
			return joinText(segments)
		}
	}
	return panel.Plain(line.Raw)
}

// renderLogLine draws one log line, cut to width, with its marks.
func renderLogLine(line logs.LogLine, structured bool, marks lineMarks, width int) string {
	budget := width - 1
	if width <= 0 {
		budget = 1 << 20
	}
	var segments []segment
	if structured && line.Record != nil {
		segments = structuredSegments(line.Record, budget)
	}
	if len(segments) == 0 {
		segments = rawSegments(line.Raw, width)
	}
	return paint(segments, marks, budget)
}

// structuredSegments lays a record out as timestamp, level, message and the
// remaining fields, within budget cells.
func structuredSegments(record *logs.Record, budget int) []segment {
	renderer := structuredLineRenderer{budget: budget}
	if timestamp, ok := record.Timestamp(); ok {
		renderer.emit(timestamp+" ", theme.Dim)
	}
	if level, ok := record.Level(); ok {
		renderer.emit(fmt.Sprintf("%-5s ", level), levelStyle(level).Bold(true))
	}
	if message, ok := record.Message(); ok {
		renderer.emit(message, lipgloss.NewStyle())
	}
	for _, field := range record.Fields() {
		if !logs.IsWellKnownKey(field.Key) {
			renderer.emit(" "+field.Key+"="+field.Value, theme.Dim)
		}
	}
	return renderer.segments
}

// segment is a run of text drawn in one style.
type segment struct {
	text  string
	style lipgloss.Style
}

// structuredLineRenderer lays a record out as styled segments within the
// width budget. Highlighting happens afterwards, over the line as drawn, so a
// match is marked wherever it shows — in a field, in the timestamp, or across
// the boundary between two parts.
type structuredLineRenderer struct {
	segments []segment
	budget   int
}

// emit adds a segment and spends its width from the budget. Width is cells,
// not characters: a CJK character or an emoji takes two, and a line cut by
// counting characters comes out twice the width it was given, which beside
// the stats panel wraps it onto a second row and pushes the footer off the
// screen.
func (r *structuredLineRenderer) emit(text string, style lipgloss.Style) {
	text = panel.Plain(text)
	if r.budget <= 0 || text == "" {
		return
	}
	if width := ansi.StringWidth(text); width > r.budget {
		text = ansi.Truncate(text, r.budget, "…")
		r.budget = 0
	} else {
		r.budget -= width
	}
	r.segments = append(r.segments, segment{text, style})
}

func rawSegments(line string, width int) []segment {
	line = panel.Plain(line)
	if width > 1 && ansi.StringWidth(line) > width-1 {
		line = ansi.Truncate(line, width-1, "…")
	}
	return []segment{{line, lipgloss.NewStyle()}}
}

// paint draws the segments with their marks. On the selected line every
// segment is drawn in the cursor's fill instead of its own colours, and the
// fill runs on to width — each segment rendered whole in one style, so the
// bar never ends where some colour inside it resets. The hits keep their own
// colours on top of it: the line the cursor is on is most often the one the
// search just stopped on.
func paint(segments []segment, marks lineMarks, width int) string {
	if marks.fill != nil {
		// One segment for the whole bar: its parts share the fill now, and
		// drawn apart they would be one bar in several pieces.
		line := joinText(segments)
		if pad := width - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		segments = []segment{{line, *marks.fill}}
	}
	return highlightIn(segments, marks.query, marks.current)
}

// highlightIn renders the segments in their styles, with every occurrence of
// query in the text they make up together drawn as a match instead — the
// first one as the current match when current is set.
func highlightIn(segments []segment, query string, current bool) string {
	text := joinText(segments)
	matches := findMatches(text, query)
	currentAt := -1
	if current && len(matches) > 0 {
		currentAt = matches[0][0]
	}
	var b strings.Builder
	start := 0
	for _, seg := range segments {
		end := start + len(seg.text)
		matches = paintSpan(&b, text[:end], start, seg.style, matches, currentAt)
		start = end
	}
	return b.String()
}

// findMatches is the byte ranges of query in text, in order and not
// overlapping.
func findMatches(text, query string) [][2]int {
	if query == "" {
		return nil
	}
	var matches [][2]int
	for offset := 0; ; {
		index := logs.FindASCIICI(text[offset:], query)
		if index < 0 {
			return matches
		}
		matches = append(matches, [2]int{offset + index, offset + index + len(query)})
		offset += index + len(query)
	}
}

// paintSpan writes text[start:] — one segment's part of the line — as runs of
// its own style and of the matches that fall in it, a match crossing into the
// next segment cut where this one ends. It returns the matches not yet behind
// it, for the next segment to go on from.
func paintSpan(b *strings.Builder, text string, start int, style lipgloss.Style,
	matches [][2]int, currentAt int) [][2]int {
	for at := start; at < len(text); {
		for len(matches) > 0 && matches[0][1] <= at {
			matches = matches[1:]
		}
		runStyle, stop := runAt(at, len(text), style, matches, currentAt)
		b.WriteString(runStyle.Render(text[at:stop]))
		at = stop
	}
	return matches
}

// runAt is the style of the run starting at `at` and where it stops: a match,
// up to its end, or the segment's own style up to the next match.
func runAt(at, end int, style lipgloss.Style, matches [][2]int, currentAt int) (lipgloss.Style, int) {
	if len(matches) == 0 {
		return style, end
	}
	next := matches[0]
	switch {
	case next[0] > at:
		return style, min(next[0], end)
	case next[0] == currentAt:
		return theme.MatchCurrent, min(next[1], end)
	default:
		return theme.Match, min(next[1], end)
	}
}

// joinText is the line the segments make up.
func joinText(segments []segment) string {
	var text strings.Builder
	for _, seg := range segments {
		text.WriteString(seg.text)
	}
	return text.String()
}

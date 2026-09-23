package follow

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

func (m *Model) structuredRendering() bool {
	if m.structured != nil {
		return *m.structured
	}
	return m.store.LooksStructured()
}

// View renders the followed feed.
func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(theme.Bold.Render(" linqode "))
	b.WriteString(terminalText(m.target))
	b.WriteString("  ")
	b.WriteString(theme.Cyan.Render(terminalText(m.title)))
	if m.structuredRendering() {
		b.WriteString(theme.Magenta.Render("  · json"))
	}
	if m.follow {
		b.WriteString(theme.Green.Render("  · following"))
	}
	b.WriteString("\n")
	statsOn := m.showStats && m.width > statsWidth+20
	logWidth := m.width
	if statsOn {
		logWidth = m.width - statsWidth
	}
	body := m.logBody(logWidth)
	if statsOn {
		// The log is padded out to its width so the panel stands at the
		// right edge. Joined as it was, the panel began wherever the longest
		// line on screen ended, which for a log of short lines put the counts
		// straight after the text, and moved them as the lines scrolled by.
		body = lipgloss.NewStyle().Width(logWidth).Render(body)
		panel := lipgloss.NewStyle().Width(statsWidth).PaddingLeft(1).Render(m.statsView())
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, panel)
	}
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *Model) logBody(width int) string {
	if m.follow {
		m.scroll = m.maxScroll()
	} else {
		m.scroll = min(m.scroll, m.maxScroll())
	}
	if m.store.Len() == 0 {
		message := "(waiting for logs …)"
		switch {
		case m.store.Filter() != nil && m.store.Total() > 0:
			message = "(no lines match the filter)"
		case m.ended:
			message = "(no log output)"
		}
		return theme.Dim.Render("  " + message)
	}
	structured := m.structuredRendering()
	var lines []string
	for i := m.scroll; i < min(m.store.Len(), m.scroll+m.viewport); i++ {
		line, _ := m.store.Line(i)
		lines = append(lines, renderLogLine(line, structured, m.query, width))
	}
	return strings.Join(lines, "\n")
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
	if m.statsFocus {
		cursor = m.cursorIndex(m.picksOf(levels, values))
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
	return m.picksOf(levels, values)
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

func (m *Model) footer() string {
	if m.input != inputNone {
		return m.inputFooter()
	}
	if m.notice != "" {
		return theme.Yellow.Render(" " + terminalText(m.notice))
	}
	if m.ended {
		return m.endedFooter()
	}
	return m.activeFooter()
}

func (m *Model) inputFooter() string {
	prompt, hint := "", ""
	switch m.input {
	case inputSearch:
		prompt, hint = " /", "  enter search · esc cancel"
	case inputFilter:
		prompt, hint = " filter: ", `  key=value key!=value key="a b" · empty clears · esc cancel`
	}
	return prompt + terminalText(m.inputText) + "▏" + theme.Dim.Render(hint)
}

func (m *Model) endedFooter() string {
	text := "log stream ended"
	style := theme.Yellow
	if m.exitCode > 0 {
		text = fmt.Sprintf("log stream ended (exit %d)", m.exitCode)
		style = theme.Red
	}
	out := style.Render(" " + text)
	if m.stderrNotice != "" {
		out += theme.Red.Render("  · " + terminalText(m.stderrNotice))
	}
	return out
}

// activeFooter is the dashboard's footer, built by the same function: what
// works wherever the keys are on the left, a rule, then what the view is
// showing and what the region with the keys answers to. A keymap laid out
// one way on one screen and another way on the next is one an operator
// reads twice.
func (m *Model) activeFooter() string {
	// What narrows the view, and nothing else: how many lines it holds is the
	// stats panel's first row, and said twice it was one more thing on a line
	// that has to fit the keys.
	var parts []string
	if m.store.Filter() != nil {
		parts = append(parts, theme.Cyan.Render("f:"+terminalText(m.store.Filter().Expr())))
	}
	if m.query != "" {
		parts = append(parts, theme.Yellow.Render("/"+terminalText(m.query)))
	}
	return panel.Footer(m.globalHints(), strings.Join(parts, "  "), m.focusedHints(), m.width)
}

// globalHints work wherever the keys are, the panel's cursor included. `tab`
// is offered only while there is a panel to move the keys to, as on the
// dashboard.
func (m *Model) globalHints() []panel.Hint {
	var hints []panel.Hint
	if m.showStats {
		hints = append(hints, panel.Hint{Text: "tab panels", Drop: 7})
	}
	return append(hints,
		panel.Hint{Text: "/ search", Drop: 3},
		panel.Hint{Text: "a stats", Drop: 2},
		panel.Hint{Text: "q quit", Drop: 0})
}

// focusedHints are what the log or the panel answers to. Editing the filter
// and resetting are offered only once there is something to edit or reset;
// `s` is not offered at all, since detection gets it right and the key is a
// correction for when it does not.
func (m *Model) focusedHints() []panel.Hint {
	var hints []panel.Hint
	if m.statsFocus {
		hints = []panel.Hint{{Text: "esc log", Drop: 5},
			{Text: "↑↓ move", Drop: 4}, {Text: "enter filter", Drop: 1}, {Text: "t field", Drop: 6}}
	} else {
		hints = []panel.Hint{{Text: "esc back", Drop: 1}}
		if m.query != "" {
			hints = append(hints, panel.Hint{Text: "n/N next", Drop: 6})
		}
		if m.store.Filter() != nil {
			hints = append(hints, panel.Hint{Text: "f filter", Drop: 5})
		}
	}
	if m.store.Filter() != nil || m.query != "" {
		hints = append(hints, panel.Hint{Text: "x reset", Drop: 4})
	}
	return hints
}

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

func renderLogLine(line logs.LogLine, structured bool, query string, width int) string {
	if !structured || line.Record == nil {
		return renderRaw(line.Raw, query, width)
	}
	renderer := structuredLineRenderer{budget: width - 1, query: query}
	if width <= 0 {
		renderer.budget = 1 << 20
	}
	record := line.Record
	if timestamp, ok := record.Timestamp(); ok {
		renderer.emit(timestamp+" ", theme.Dim, false)
	}
	if level, ok := record.Level(); ok {
		renderer.emit(fmt.Sprintf("%-5s ", level), levelStyle(level).Bold(true), false)
	}
	if message, ok := record.Message(); ok {
		renderer.emit(message, lipgloss.NewStyle(), true)
	}
	for _, field := range record.Fields() {
		if !logs.IsWellKnownKey(field.Key) {
			renderer.emit(" "+field.Key+"="+field.Value, theme.Dim, false)
		}
	}
	if renderer.builder.Len() == 0 {
		return renderRaw(line.Raw, query, width)
	}
	return renderer.builder.String()
}

type structuredLineRenderer struct {
	builder strings.Builder
	budget  int
	query   string
}

func (r *structuredLineRenderer) emit(text string, style lipgloss.Style, highlighted bool) {
	text = terminalText(text)
	if r.budget <= 0 || text == "" {
		return
	}
	if runes := []rune(text); len(runes) > r.budget {
		text = string(runes[:max(r.budget-1, 0)]) + "…"
		r.budget = 0
	} else {
		r.budget -= len(runes)
	}
	if highlighted && r.query != "" {
		r.builder.WriteString(highlightIn(text, r.query, style))
	} else {
		r.builder.WriteString(style.Render(text))
	}
}

func renderRaw(line, query string, width int) string {
	line = terminalText(line)
	if width > 1 {
		if runes := []rune(line); len(runes) > width-1 {
			line = string(runes[:width-2]) + "…"
		}
	}
	if query == "" {
		return line
	}
	return highlightIn(line, query, lipgloss.NewStyle())
}

// terminalText treats remote output as one line of text, never as terminal
// instructions. Sanitize before adding our own styles, leaving the stored
// record intact for search, filters, and statistics.
func terminalText(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func highlightIn(text, query string, base lipgloss.Style) string {
	var b strings.Builder
	rest := text
	for {
		index := logs.FindASCIICI(rest, query)
		if index < 0 {
			break
		}
		if index > 0 {
			b.WriteString(base.Render(rest[:index]))
		}
		b.WriteString(theme.Match.Render(rest[index : index+len(query)]))
		rest = rest[index+len(query):]
	}
	if b.Len() == 0 {
		return base.Render(text)
	}
	if rest != "" {
		b.WriteString(base.Render(rest))
	}
	return b.String()
}

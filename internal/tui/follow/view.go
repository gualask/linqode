package follow

import (
	"fmt"
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

// statsView is the side panel: how many lines, when they were written, and
// how they divide between levels and between the values of a chosen field.
//
// It never draws more rows than the log beside it has. It used to draw as
// many as it had counts, and on a short terminal that pushed the footer off
// the bottom of the screen; now it is cut from the bottom, where the least
// frequent values are.
func (m *Model) statsView() string {
	width := statsWidth - 1 // the panel's left padding
	stats := m.store.ComputeStats(m.topField)
	lines := []string{
		fmt.Sprintf("%d lines · %s", stats.Total,
			theme.Magenta.Render(fmt.Sprintf("%d json", stats.Parsed))),
		"",
	}
	lines = append(lines, m.timelineView(width)...)
	lines = append(lines, "", theme.Bold.Render("levels"))
	if len(stats.Levels) == 0 {
		lines = append(lines, theme.Dim.Render("  (none)"))
	}
	for _, level := range stats.Levels {
		style := levelStyle(level.Key)
		lines = append(lines, countRow(level, total(stats.Levels), width, style, style))
	}
	lines = append(lines, "")
	if m.topField == "" {
		lines = append(lines, theme.Dim.Render("t: pick a top field"))
	} else {
		lines = append(lines, theme.Bold.Render("top "+terminalText(m.topField)))
		values := stats.Values
		if len(values) > topValues {
			values = values[:topValues]
		}
		if len(values) == 0 {
			lines = append(lines, theme.Dim.Render("  (no values)"))
		}
		for _, value := range values {
			lines = append(lines, countRow(value, total(stats.Values), width,
				theme.Cyan, lipgloss.NewStyle()))
		}
	}
	if m.viewport > 0 && len(lines) > m.viewport {
		lines = lines[:m.viewport]
	}
	return strings.Join(lines, "\n")
}

// countRow is one count, its share of the list it belongs to as a bar, and
// what it counts. The bar is the share of every count in the list, not of
// the largest one, so a list's bars add up to one whole bar: that is what
// makes "most of it" and "a sliver" readable without doing the sums.
func countRow(count logs.Count, of, width int, bar, name lipgloss.Style) string {
	share := 0.0
	if of > 0 {
		share = float64(count.N) / float64(of)
	}
	label := ansi.Truncate(terminalText(count.Key), max(width-7-statsBar-1, 1), "…")
	return fmt.Sprintf("%6d %s %s", count.N, bar.Render(spark.Bar(share, statsBar)), name.Render(label))
}

func total(counts []logs.Count) int {
	sum := 0
	for _, count := range counts {
		sum += count.N
	}
	return sum
}

// timelineView is when the lines in view were written: a histogram whose
// height is how many and whose colour is the worst level among them, over an
// axis saying how far back it reaches and which clock placed the lines.
//
// Height says how much and colour says how bad, the rule every strip on the
// home follows. A slice holding one error among five hundred lines is drawn
// red whole, because when the errors happened is what the panel is opened to
// find out; how many there were is the levels list underneath.
func (m *Model) timelineView(width int) []string {
	header := theme.Bold.Render("timeline")
	if m.store.Filter() != nil {
		// The counts below are of the whole tail; this is of the lines the
		// filter lets through, and it must not be read as the same thing.
		header += theme.Dim.Render(" · filtered")
	}
	timeline := m.store.Timeline(m.now(), width)
	if timeline.Placed == 0 {
		return []string{header, theme.Dim.Render("  (nothing to place yet)")}
	}
	values := make([]float64, len(timeline.Buckets))
	ceiling := 0.0
	for index, bucket := range timeline.Buckets {
		values[index] = float64(bucket.Lines)
		ceiling = max(ceiling, values[index])
	}
	lines := []string{header}
	lines = append(lines, spark.Columns(values, ceiling, m.timelineRows(), func(index int) lipgloss.Style {
		return severityStyle(timeline.Buckets[index].Worst)
	})...)

	clock := "by arrival"
	if timeline.Clock == logs.ByLogTime {
		clock = "by log time"
	}
	left := "-" + formatSpan(timeline.Span)
	gap := max(width-len(left)-len(clock)-len("now"), 2)
	axis := left + strings.Repeat(" ", gap/2) + clock + strings.Repeat(" ", gap-gap/2) + "now"
	lines = append(lines, theme.Dim.Render(axis))
	if timeline.Older > 0 {
		lines = append(lines, theme.Dim.Render(fmt.Sprintf("%d older lines not drawn", timeline.Older)))
	}
	return lines
}

// timelineRows is how tall the histogram is drawn: one row on a terminal
// with room for little else, growing a row for every four the log has, up
// to four. Past that a histogram adds resolution nobody reads and takes the
// rows the counts under it need.
func (m *Model) timelineRows() int {
	return min(max((m.viewport-10)/4, 1), 4)
}

// severityStyle colours a slice of the timeline by the worst line in it.
// Everything short of a warning is one colour, the one every throughput strip
// on the home is drawn in, so the two colours that mean something are the
// only ones that stand out.
func severityStyle(severity logs.Severity) lipgloss.Style {
	switch severity {
	case logs.SeverityError:
		return theme.Red
	case logs.SeverityWarning:
		return theme.Yellow
	default:
		return theme.Cyan
	}
}

// formatSpan writes a timeline's span the shortest way: 15m, 6h, 7d.
func formatSpan(span time.Duration) string {
	switch {
	case span%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(span/(24*time.Hour)))
	case span%time.Hour == 0:
		return fmt.Sprintf("%dh", int(span/time.Hour))
	default:
		return fmt.Sprintf("%dm", int(span/time.Minute))
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
		prompt, hint = " filter: ", "  key=value key!=value · empty clears · esc cancel"
	case inputTopField:
		prompt, hint = " top field: ", "  empty clears · esc cancel"
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

func (m *Model) activeFooter() string {
	var out string
	if m.store.Filter() != nil {
		out = fmt.Sprintf(" %d/%d lines", m.store.Len(), m.store.Total())
		out += theme.Cyan.Render("  f:" + terminalText(m.store.Filter().Expr()))
	} else {
		out = fmt.Sprintf(" %d lines", m.store.Len())
	}
	if m.query != "" {
		out += theme.Yellow.Render("  /" + terminalText(m.query))
	}
	// The keys are marked here the way they are on the home: the character to
	// press in the accent, the word recessive. A keymap spelled one way on
	// one screen and another way on the next is one an operator reads twice.
	// The prompts above are left alone — `empty clears` is not a key.
	//
	// `q quit` keeps the global grey for the same reason. This view has no
	// divider to separate the two kinds — it is one region, and everything
	// else here is its own — but the colour still means what it means
	// everywhere else, and a key that works from anywhere must not change
	// colour depending on which screen it is read from.
	return out + theme.Dim.Render("  ·  ") +
		panel.MarkKeys("/ search · f filter · s json · a stats · t field · esc back", true) +
		theme.Dim.Render(" · ") + panel.MarkKeys("q quit", false)
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

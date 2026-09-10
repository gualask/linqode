package follow

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/tui/panel"
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

func (m *Model) statsView() string {
	stats := m.store.ComputeStats(m.topField)
	lines := []string{
		fmt.Sprintf("%d lines · %s", stats.Total,
			theme.Magenta.Render(fmt.Sprintf("%d json", stats.Parsed))),
		"", theme.Bold.Render("levels"),
	}
	if len(stats.Levels) == 0 {
		lines = append(lines, theme.Dim.Render("  (none)"))
	}
	for _, level := range stats.Levels {
		lines = append(lines, fmt.Sprintf("%7d  %s", level.N, levelStyle(level.Key).Render(terminalText(level.Key))))
	}
	lines = append(lines, "")
	if m.topField == "" {
		return strings.Join(append(lines, theme.Dim.Render("t: pick a top field")), "\n")
	}
	lines = append(lines, theme.Bold.Render("top "+terminalText(m.topField)))
	values := stats.Values
	if len(values) > topValues {
		values = values[:topValues]
	}
	if len(values) == 0 {
		lines = append(lines, theme.Dim.Render("  (no values)"))
	}
	for _, value := range values {
		lines = append(lines, fmt.Sprintf("%7d  %s", value.N, terminalText(value.Key)))
	}
	return strings.Join(lines, "\n")
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
	switch strings.ToLower(level) {
	case "error", "fatal", "critical", "panic":
		return theme.Red
	case "warn", "warning":
		return theme.Yellow
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

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/logs"
)

func (m *logsModel) structuredRendering() bool {
	if m.structured != nil {
		return *m.structured
	}
	return m.store.LooksStructured()
}

func (m *logsModel) view() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render(" linqode "))
	b.WriteString(m.target)
	b.WriteString("  ")
	b.WriteString(cyanStyle.Render(m.title))
	if m.structuredRendering() {
		b.WriteString(magentaStyle.Render("  · json"))
	}
	if m.follow {
		b.WriteString(greenStyle.Render("  · following"))
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

func (m *logsModel) logBody(width int) string {
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
		return dimStyle.Render("  " + message)
	}
	structured := m.structuredRendering()
	var lines []string
	for i := m.scroll; i < min(m.store.Len(), m.scroll+m.viewport); i++ {
		line, _ := m.store.Line(i)
		lines = append(lines, renderLogLine(line, structured, m.query, width))
	}
	return strings.Join(lines, "\n")
}

func (m *logsModel) statsView() string {
	stats := m.store.ComputeStats(m.topField)
	lines := []string{
		fmt.Sprintf("%d lines · %s", stats.Total,
			magentaStyle.Render(fmt.Sprintf("%d json", stats.Parsed))),
		"", boldStyle.Render("levels"),
	}
	if len(stats.Levels) == 0 {
		lines = append(lines, dimStyle.Render("  (none)"))
	}
	for _, level := range stats.Levels {
		lines = append(lines, fmt.Sprintf("%7d  %s", level.N, levelStyle(level.Key).Render(level.Key)))
	}
	lines = append(lines, "")
	if m.topField == "" {
		return strings.Join(append(lines, dimStyle.Render("t: pick a top field")), "\n")
	}
	lines = append(lines, boldStyle.Render("top "+m.topField))
	values := stats.Values
	if len(values) > topValues {
		values = values[:topValues]
	}
	if len(values) == 0 {
		lines = append(lines, dimStyle.Render("  (no values)"))
	}
	for _, value := range values {
		lines = append(lines, fmt.Sprintf("%7d  %s", value.N, value.Key))
	}
	return strings.Join(lines, "\n")
}

func (m *logsModel) footer() string {
	if m.input != inputNone {
		return m.inputFooter()
	}
	if m.notice != "" {
		return yellowStyle.Render(" " + m.notice)
	}
	if m.ended {
		return m.endedFooter()
	}
	return m.activeFooter()
}

func (m *logsModel) inputFooter() string {
	prompt, hint := "", ""
	switch m.input {
	case inputSearch:
		prompt, hint = " /", "  enter search · esc cancel"
	case inputFilter:
		prompt, hint = " filter: ", "  key=value key!=value · empty clears · esc cancel"
	case inputTopField:
		prompt, hint = " top field: ", "  empty clears · esc cancel"
	}
	return prompt + m.inputText + "▏" + dimStyle.Render(hint)
}

func (m *logsModel) endedFooter() string {
	text := "log stream ended"
	style := yellowStyle
	if m.exitCode > 0 {
		text = fmt.Sprintf("log stream ended (exit %d)", m.exitCode)
		style = redStyle
	}
	out := style.Render(" " + text)
	if m.stderrNotice != "" {
		out += redStyle.Render("  · " + m.stderrNotice)
	}
	return out
}

func (m *logsModel) activeFooter() string {
	var out string
	if m.store.Filter() != nil {
		out = fmt.Sprintf(" %d/%d lines", m.store.Len(), m.store.Total())
		out += cyanStyle.Render("  f:" + m.store.Filter().Expr())
	} else {
		out = fmt.Sprintf(" %d lines", m.store.Len())
	}
	if m.query != "" {
		out += yellowStyle.Render("  /" + m.query)
	}
	return out + dimStyle.Render("  ·  / search · f filter · s json · a stats · t field · esc back")
}

func levelStyle(level string) lipgloss.Style {
	switch strings.ToLower(level) {
	case "error", "fatal", "critical", "panic":
		return redStyle
	case "warn", "warning":
		return yellowStyle
	case "info":
		return greenStyle
	case "debug":
		return blueStyle
	case "trace":
		return dimStyle
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
		renderer.emit(timestamp+" ", dimStyle, false)
	}
	if level, ok := record.Level(); ok {
		renderer.emit(fmt.Sprintf("%-5s ", level), levelStyle(level).Bold(true), false)
	}
	if message, ok := record.Message(); ok {
		renderer.emit(message, lipgloss.NewStyle(), true)
	}
	for _, field := range record.Fields() {
		if !logs.IsWellKnownKey(field.Key) {
			renderer.emit(" "+field.Key+"="+field.Value, dimStyle, false)
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
		b.WriteString(matchStyle.Render(rest[index : index+len(query)]))
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

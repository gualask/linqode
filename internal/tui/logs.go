package tui

// Log follow view: the tail of a followed remote command (logs, later
// actions and scripts), with follow mode, scrollback, `/` search, and
// structured-log analysis — JSONL detection, `key=value` field filters,
// and a live stats panel (counts by level, top values of a field).
// Feed events are drained on a timer with an upper bound per tick, so a
// log burst cannot starve input handling.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/logs"
)

const (
	// logCapacity is the scrollback kept in memory per followed command.
	logCapacity = 10_000
	// maxEventsPerTick bounds how many feed events one drain applies.
	maxEventsPerTick = 5_000
	drainInterval    = 100 * time.Millisecond
	// statsWidth is the width of the stats side panel; topValues is how
	// many top values it lists.
	statsWidth = 28
	topValues  = 8
)

type drainTickMsg struct{}

func drainTick() tea.Cmd {
	return tea.Tick(drainInterval, func(time.Time) tea.Msg { return drainTickMsg{} })
}

// inputMode says which prompt the footer input line is collecting.
type inputMode int

const (
	inputNone inputMode = iota
	// inputSearch is `/` — text search over raw lines.
	inputSearch
	// inputFilter is `f` — field filter expression (`level=error app!=web`).
	inputFilter
	// inputTopField is `t` — field whose top values the stats panel counts.
	inputTopField
)

type logsModel struct {
	target string
	// title labels the header: `logs: web`, later `restart: web`, …
	title string
	feed  LogFeed
	store *logs.Store

	// scroll is the index of the top visible line; recomputed on view when
	// following.
	follow   bool
	scroll   int
	viewport int
	width    int
	height   int

	feedDone bool // events channel closed
	ended    bool
	exitCode int // -1 = none reported
	// stderrNotice is the last stderr line from the remote command
	// (compose diagnostics).
	stderrNotice string

	input     inputMode
	inputText string
	query     string
	matchLine int // current match, anchor for n/N; -1 = none
	notice    string

	// structured: nil follows JSONL auto-detection, otherwise a manual
	// override (`s`).
	structured *bool
	showStats  bool
	topField   string
}

func newLogsModel(target, title string, feed LogFeed) logsModel {
	return logsModel{
		target:    target,
		title:     title,
		feed:      feed,
		store:     logs.NewStore(logCapacity),
		follow:    true,
		exitCode:  -1,
		matchLine: -1,
	}
}

func (m *logsModel) init() tea.Cmd {
	return drainTick()
}

func (m *logsModel) setSize(width, height int) {
	m.width, m.height = width, height
	m.viewport = max(height-2, 1) // header (1) + footer (1)
}

func (m *logsModel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case drainTickMsg:
		m.drain()
		if m.feedDone {
			return nil
		}
		return drainTick()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return nil
}

// drain applies pending feed events to the store, at most
// maxEventsPerTick.
func (m *logsModel) drain() {
	for range maxEventsPerTick {
		select {
		case ev, ok := <-m.feed.Events:
			if !ok {
				m.feedDone = true
				m.ended = true
				return
			}
			switch ev.Kind {
			case LogLine:
				if dropped := m.store.Push(ev.Text); dropped > 0 {
					m.scroll = max(m.scroll-dropped, 0)
					if m.matchLine >= 0 {
						m.matchLine = max(m.matchLine-dropped, -1)
					}
				}
			case LogStderrLine:
				m.stderrNotice = ev.Text
			case LogEnded:
				m.ended = true
				m.exitCode = ev.ExitCode
			}
		default:
			return
		}
	}
}

func (m *logsModel) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.input != inputNone {
		switch key.String() {
		case "esc":
			m.input = inputNone
			m.inputText = ""
		case "enter":
			mode, text := m.input, m.inputText
			m.input = inputNone
			m.inputText = ""
			m.commitInput(mode, text)
		case "backspace":
			if r := []rune(m.inputText); len(r) > 0 {
				m.inputText = string(r[:len(r)-1])
			}
		default:
			if len(key.Runes) > 0 {
				m.inputText += string(key.Runes)
			}
		}
		return nil
	}

	switch key.String() {
	case "q", "esc":
		return func() tea.Msg { return closeFollowMsg{} }
	case "ctrl+c":
		return tea.Quit
	case "j", "down":
		m.scrollBy(1)
	case "k", "up":
		m.scrollBy(-1)
	case "pgdown":
		m.scrollBy(m.viewport)
	case "pgup":
		m.scrollBy(-m.viewport)
	case "g", "home":
		m.follow = false
		m.scroll = 0
	case "G", "end":
		m.follow = true
	case "/":
		m.input, m.inputText = inputSearch, ""
	case "n":
		m.nextMatch(true)
	case "N":
		m.nextMatch(false)
	case "f":
		current := ""
		if f := m.store.Filter(); f != nil {
			current = f.Expr()
		}
		m.input, m.inputText = inputFilter, current
	case "s":
		effective := !m.structuredRendering()
		m.structured = &effective
	case "a":
		m.showStats = !m.showStats
	case "t":
		m.input, m.inputText = inputTopField, m.topField
	}
	return nil
}

func (m *logsModel) commitInput(mode inputMode, text string) {
	m.notice = ""
	switch mode {
	case inputSearch:
		m.matchLine = -1
		m.query = text
		if m.query != "" {
			m.jump(true, m.scroll)
		}
	case inputFilter:
		f, err := logs.ParseFilter(text)
		if err != nil {
			m.notice = "bad filter: " + err.Error()
			return
		}
		m.store.SetFilter(f)
		m.matchLine = -1
		m.follow = true
	case inputTopField:
		m.topField = text
		m.showStats = true
	}
}

// structuredRendering says whether lines render in parsed JSONL form.
func (m *logsModel) structuredRendering() bool {
	if m.structured != nil {
		return *m.structured
	}
	return m.store.LooksStructured()
}

func (m *logsModel) maxScroll() int {
	return max(m.store.Len()-m.viewport, 0)
}

func (m *logsModel) scrollBy(delta int) {
	limit := m.maxScroll()
	m.scroll = min(max(m.scroll+delta, 0), limit)
	// Scrolling up leaves follow mode; hitting bottom re-enters it.
	m.follow = delta > 0 && m.scroll >= limit
}

// jump searches from `from` and scrolls to the match.
func (m *logsModel) jump(forward bool, from int) {
	if m.query == "" {
		m.notice = "no search query (use /)"
		return
	}
	var index int
	var found bool
	if forward {
		index, found = m.store.SearchNext(m.query, from)
	} else {
		index, found = m.store.SearchPrev(m.query, from)
	}
	if !found {
		m.notice = fmt.Sprintf("no match for `%s`", m.query)
		return
	}
	m.matchLine = index
	m.follow = false
	m.scroll = max(index-m.viewport/3, 0)
	m.notice = ""
}

func (m *logsModel) nextMatch(forward bool) {
	n := m.store.Len()
	if n == 0 {
		return
	}
	from := m.scroll
	if m.matchLine >= 0 {
		if forward {
			from = (m.matchLine + 1) % n
		} else {
			from = m.matchLine - 1
			if from < 0 {
				from = n - 1
			}
		}
	}
	m.jump(forward, from)
}

func (m *logsModel) view() string {
	var b strings.Builder

	// Header.
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

	// Body: log lines, with the stats panel at the right when open.
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

	// Footer.
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
	var lines []string
	lines = append(lines,
		fmt.Sprintf("%d lines · %s", stats.Total,
			magentaStyle.Render(fmt.Sprintf("%d json", stats.Parsed))),
		"",
		boldStyle.Render("levels"))
	if len(stats.Levels) == 0 {
		lines = append(lines, dimStyle.Render("  (none)"))
	}
	for _, level := range stats.Levels {
		lines = append(lines, fmt.Sprintf("%7d  %s",
			level.N, levelStyle(level.Key).Render(level.Key)))
	}
	lines = append(lines, "")
	if m.topField == "" {
		lines = append(lines, dimStyle.Render("t: pick a top field"))
	} else {
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
	}
	return strings.Join(lines, "\n")
}

func (m *logsModel) footer() string {
	if m.input != inputNone {
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
	if m.notice != "" {
		return yellowStyle.Render(" " + m.notice)
	}
	if m.ended {
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
	out += dimStyle.Render("  ·  / search · f filter · s json · a stats · t field · esc back")
	return out
}

// levelStyle colors a JSONL severity value (any case).
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

// renderLogLine renders one line: parsed JSONL layout in structured mode,
// raw text with search highlighting otherwise. Long lines truncate to
// width.
func renderLogLine(line logs.LogLine, structured bool, query string, width int) string {
	if !structured || line.Record == nil {
		return renderRaw(line.Raw, query, width)
	}
	budget := width - 1
	if width <= 0 {
		budget = 1 << 20
	}
	var b strings.Builder
	wrote := false
	emit := func(text string, style lipgloss.Style, highlighted bool) {
		if budget <= 0 || text == "" {
			return
		}
		if r := []rune(text); len(r) > budget {
			text = string(r[:max(budget-1, 0)]) + "…"
			budget = 0
		} else {
			budget -= len(r)
		}
		if highlighted && query != "" {
			b.WriteString(highlightIn(text, query, style))
		} else {
			b.WriteString(style.Render(text))
		}
		wrote = true
	}

	record := line.Record
	if timestamp, ok := record.Timestamp(); ok {
		emit(timestamp+" ", dimStyle, false)
	}
	if level, ok := record.Level(); ok {
		emit(fmt.Sprintf("%-5s ", level), levelStyle(level).Bold(true), false)
	}
	if message, ok := record.Message(); ok {
		emit(message, lipgloss.NewStyle(), true)
	}
	for _, field := range record.Fields() {
		if !logs.IsWellKnownKey(field.Key) {
			emit(" "+field.Key+"="+field.Value, dimStyle, false)
		}
	}
	if !wrote {
		return renderRaw(line.Raw, query, width)
	}
	return b.String()
}

// renderRaw truncates a raw line to the terminal width and highlights
// every search match.
func renderRaw(line, query string, width int) string {
	if width > 1 {
		if r := []rune(line); len(r) > width-1 {
			line = string(r[:width-2]) + "…"
		}
	}
	if query == "" {
		return line
	}
	return highlightIn(line, query, lipgloss.NewStyle())
}

// highlightIn renders text in base style with every query match
// highlighted.
func highlightIn(text, query string, base lipgloss.Style) string {
	var b strings.Builder
	rest := text
	for {
		i := logs.FindASCIICI(rest, query)
		if i < 0 {
			break
		}
		if i > 0 {
			b.WriteString(base.Render(rest[:i]))
		}
		b.WriteString(matchStyle.Render(rest[i : i+len(query)]))
		rest = rest[i+len(query):]
	}
	if b.Len() == 0 {
		return base.Render(text)
	}
	if rest != "" {
		b.WriteString(base.Render(rest))
	}
	return b.String()
}

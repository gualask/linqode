package tui

// Log follow view: the tail of a followed remote command (logs, later
// actions and scripts), with follow mode, scrollback, and `/` search.
// Feed events are drained on a timer with an upper bound per tick, so a
// log burst cannot starve input handling.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/logs"
)

const (
	// logCapacity is the scrollback kept in memory per followed command.
	logCapacity = 10_000
	// maxEventsPerTick bounds how many feed events one drain applies.
	maxEventsPerTick = 5_000
	drainInterval    = 100 * time.Millisecond
)

type drainTickMsg struct{}

func drainTick() tea.Cmd {
	return tea.Tick(drainInterval, func(time.Time) tea.Msg { return drainTickMsg{} })
}

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

	searching  bool // footer input line active
	searchText string
	query      string
	matchLine  int // current match, anchor for n/N; -1 = none
	notice     string
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
	if m.searching {
		switch key.String() {
		case "esc":
			m.searching = false
			m.searchText = ""
		case "enter":
			m.searching = false
			m.notice = ""
			m.matchLine = -1
			m.query = m.searchText
			m.searchText = ""
			if m.query != "" {
				m.jump(true, m.scroll)
			}
		case "backspace":
			if r := []rune(m.searchText); len(r) > 0 {
				m.searchText = string(r[:len(r)-1])
			}
		default:
			if len(key.Runes) > 0 {
				m.searchText += string(key.Runes)
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
		m.searching = true
		m.searchText = ""
	case "n":
		m.nextMatch(true)
	case "N":
		m.nextMatch(false)
	}
	return nil
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
	if m.follow {
		b.WriteString(greenStyle.Render("  · following"))
	}
	b.WriteString("\n")

	// Body.
	if m.follow {
		m.scroll = m.maxScroll()
	} else {
		m.scroll = min(m.scroll, m.maxScroll())
	}
	if m.store.Len() == 0 {
		message := "(waiting for logs …)"
		if m.ended {
			message = "(no log output)"
		}
		b.WriteString(dimStyle.Render("  " + message))
		b.WriteString("\n")
	} else {
		for i := m.scroll; i < min(m.store.Len(), m.scroll+m.viewport); i++ {
			line, _ := m.store.Line(i)
			b.WriteString(renderLogLine(line, m.query, m.width))
			b.WriteString("\n")
		}
	}

	// Footer.
	b.WriteString(m.footer())
	return b.String()
}

func (m *logsModel) footer() string {
	if m.searching {
		return " /" + m.searchText + "▏" +
			dimStyle.Render("  enter search · esc cancel")
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
	out := fmt.Sprintf(" %d lines", m.store.Len())
	if m.query != "" {
		out += yellowStyle.Render("  /" + m.query)
	}
	out += dimStyle.Render("  ·  / search · n/N match · G follow · esc back")
	return out
}

// renderLogLine truncates a raw line to the terminal width and highlights
// every search match.
func renderLogLine(line, query string, width int) string {
	if width > 1 {
		if r := []rune(line); len(r) > width-1 {
			line = string(r[:width-2]) + "…"
		}
	}
	if query == "" {
		return line
	}
	var b strings.Builder
	rest := line
	for {
		i := logs.FindASCIICI(rest, query)
		if i < 0 {
			break
		}
		b.WriteString(rest[:i])
		b.WriteString(matchStyle.Render(rest[i : i+len(query)]))
		rest = rest[i+len(query):]
	}
	b.WriteString(rest)
	return b.String()
}

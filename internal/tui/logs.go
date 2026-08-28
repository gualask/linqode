package tui

// Log follow view: the tail of a followed remote command (logs, later
// actions and scripts), with follow mode, scrollback, `/` search, and
// structured-log analysis — JSONL detection, `key=value` field filters,
// and a live stats panel (counts by level, top values of a field).
// Feed events are drained on a timer with an upper bound per tick, so a
// log burst cannot starve input handling.

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/operations"
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
	feed  operations.Feed
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

func newLogsModel(target, title string, feed operations.Feed) logsModel {
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
			case operations.EventLog, operations.EventStdout:
				if dropped := m.store.Push(ev.Text); dropped > 0 {
					m.scroll = max(m.scroll-dropped, 0)
					if m.matchLine >= 0 {
						m.matchLine = max(m.matchLine-dropped, -1)
					}
				}
			case operations.EventStderr:
				m.stderrNotice = ev.Text
			case operations.EventExit:
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

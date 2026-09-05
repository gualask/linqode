package follow

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/logs"
)

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

func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.input != inputNone {
		return m.handleInputKey(key)
	}

	switch key.String() {
	case "esc":
		// Back to the screen this was opened from, never out of the
		// application: that is `q`, here as everywhere else.
		return func() tea.Msg { return CloseMsg{} }
	case "q", "ctrl+c":
		return tea.Quit
	case "down", "up", "pgdown", "pgup", "home", "end":
		m.handleNavigationKey(key.String())
	case "/", "n", "N", "f", "s", "a", "t":
		m.handleToolKey(key.String())
	}
	return nil
}

func (m *Model) handleNavigationKey(key string) {
	switch key {
	case "down":
		m.scrollBy(1)
	case "up":
		m.scrollBy(-1)
	case "pgdown":
		m.scrollBy(m.viewport)
	case "pgup":
		m.scrollBy(-m.viewport)
	case "home":
		m.follow = false
		m.scroll = 0
	case "end":
		m.follow = true
	}
}

func (m *Model) handleToolKey(key string) {
	switch key {
	case "/":
		m.input, m.inputText = inputSearch, ""
	case "n":
		m.nextMatch(true)
	case "N":
		m.nextMatch(false)
	case "f":
		m.openFilterInput()
	case "s":
		effective := !m.structuredRendering()
		m.structured = &effective
	case "a":
		m.showStats = !m.showStats
	case "t":
		m.input, m.inputText = inputTopField, m.topField
	}
}

func (m *Model) openFilterInput() {
	current := ""
	if filter := m.store.Filter(); filter != nil {
		current = filter.Expr()
	}
	m.input, m.inputText = inputFilter, current
}

func (m *Model) handleInputKey(key tea.KeyMsg) tea.Cmd {
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
		m.deleteInputRune()
	default:
		if len(key.Runes) > 0 {
			m.inputText += string(key.Runes)
		}
	}
	return nil
}

func (m *Model) deleteInputRune() {
	runes := []rune(m.inputText)
	if len(runes) == 0 {
		return
	}
	m.inputText = string(runes[:len(runes)-1])
}

func (m *Model) commitInput(mode inputMode, text string) {
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

func (m *Model) maxScroll() int {
	return max(m.store.Len()-m.viewport, 0)
}

func (m *Model) scrollBy(delta int) {
	limit := m.maxScroll()
	m.scroll = min(max(m.scroll+delta, 0), limit)
	// Scrolling up leaves follow mode; hitting bottom re-enters it.
	m.follow = delta > 0 && m.scroll >= limit
}

// jump searches from `from` and scrolls to the match.
func (m *Model) jump(forward bool, from int) {
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

func (m *Model) nextMatch(forward bool) {
	n := m.store.Len()
	if n == 0 {
		return
	}
	m.jump(forward, m.nextMatchStart(forward, n))
}

func (m *Model) nextMatchStart(forward bool, lineCount int) int {
	if m.matchLine < 0 {
		return m.scroll
	}
	if forward {
		return (m.matchLine + 1) % lineCount
	}
	if m.matchLine > 0 {
		return m.matchLine - 1
	}
	return lineCount - 1
}

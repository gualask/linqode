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
	// inputSearch is `/` — text search over the lines as they are drawn.
	inputSearch
	// inputFilter is `f` — field filter expression (`level=error app!=web`),
	// for what the stats panel cannot pick: a negation, a field it is not
	// counting.
	inputFilter
)

func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.input != inputNone {
		return m.handleInputKey(key)
	}
	if m.detail != nil {
		return m.handleDetailKey(key.String())
	}

	if m.statsKeys() && m.handleStatsKey(key.String()) {
		return nil
	}
	switch key.String() {
	case "esc":
		// Back to the screen this was opened from, never out of the
		// application: that is `q`, here as everywhere else.
		return func() tea.Msg { return CloseMsg{} }
	case "q", "ctrl+c":
		return tea.Quit
	case "down", "up", "pgdown", "pgup", "home", "end", "l":
		m.handleNavigationKey(key.String())
	case "enter":
		m.openDetail()
	case "/", "n", "N", "f", "x", "s", "a", "t", "tab":
		m.handleToolKey(key.String())
	}
	return nil
}

// handleStatsKey is a key while the stats panel has focus; false leaves it
// to the log. The arrows move the cursor over the counts, and enter adds
// the row under it to the filter or takes it out. esc hands the keys back
// to the log rather than leaving the view: it is the one key that means
// "out of here" everywhere, and here is the panel.
func (m *Model) handleStatsKey(key string) bool {
	switch key {
	case "up":
		m.moveCursor(-1)
	case "down":
		m.moveCursor(1)
	case "enter":
		m.togglePicked()
	case "esc":
		m.statsFocus = false
	default:
		return false
	}
	return true
}

// handleNavigationKey moves the log's cursor; the view scrolls only when the
// cursor would leave it.
func (m *Model) handleNavigationKey(key string) {
	switch key {
	case "down":
		m.moveSelection(1)
	case "up":
		m.moveSelection(-1)
	case "pgdown":
		m.moveSelection(m.viewport)
	case "pgup":
		m.moveSelection(-m.viewport)
	case "home":
		m.follow = false
		m.selected, m.scroll = 0, 0
	case "end", "l":
		// l for live: back on the tail, following. end does the same, but a
		// laptop keyboard has no end key.
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
	case "x":
		// Everything that narrows or marks the view, at once: the filter,
		// which the panel takes out only a row at a time, and the search.
		if m.store.Filter() != nil || m.query != "" {
			m.store.SetFilter(nil)
			m.query = ""
			m.matchLine = -1
			m.follow = true
		}
	case "s":
		// Off the footer: detection gets it right, and this is the
		// correction for when it does not.
		effective := !m.structuredRendering()
		m.structured = &effective
	case "a":
		// Opening the panel hands it the keys, since it is opened to be read
		// and picked from; closing it hands them back.
		m.showStats = !m.showStats
		m.statsFocus = m.showStats
	case "tab":
		m.statsFocus = m.showStats && !m.statsFocus
	case "t":
		m.nextField()
	}
}

// nextField steps the panel to the next field worth counting by, in the
// order the log engine ranks them, wrapping round.
func (m *Model) nextField() {
	fields := m.store.Fields()
	if len(fields) == 0 {
		m.notice = "no fields to count by"
		return
	}
	next := fields[0]
	for index, field := range fields {
		if field == m.topField {
			next = fields[(index+1)%len(fields)]
			break
		}
	}
	m.topField = next
	m.showStats = true
}

// moveCursor moves the panel's cursor by delta rows, stopping at the ends.
func (m *Model) moveCursor(delta int) {
	picks := m.picks()
	if len(picks) == 0 {
		return
	}
	m.cursor = min(max(m.cursorIndex(picks)+delta, 0), len(picks)-1)
	m.picked = picks[m.cursor]
}

// togglePicked adds the row under the cursor to the filter, or takes it out.
func (m *Model) togglePicked() {
	picks := m.picks()
	if len(picks) == 0 {
		return
	}
	m.cursor = m.cursorIndex(picks)
	m.picked = picks[m.cursor]
	m.store.SetFilter(m.store.Filter().Toggle(m.picked.field, m.picked.value))
	m.matchLine = -1
	m.follow = true
	m.notice = ""
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
			m.firstMatch()
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
	}
}

func (m *Model) maxScroll() int {
	return max(m.store.Len()-m.viewport, 0)
}

func (m *Model) moveSelection(delta int) {
	last := m.store.Len() - 1
	if last < 0 {
		return
	}
	m.selected = min(max(m.selected+delta, 0), last)
	// Moving up leaves follow mode; reaching the newest line re-enters it.
	m.follow = delta > 0 && m.selected == last
	m.keepSelectionInView()
}

// keepSelectionInView scrolls just enough for the cursor to be on screen.
func (m *Model) keepSelectionInView() {
	if m.selected < m.scroll {
		m.scroll = m.selected
	} else if m.selected >= m.scroll+m.viewport {
		m.scroll = m.selected - m.viewport + 1
	}
	m.scroll = min(max(m.scroll, 0), m.maxScroll())
}

// cursorLine is the line under the log's cursor. While following that is the
// newest line, whatever the last frame left in selected.
func (m *Model) cursorLine() int {
	if m.follow {
		return max(m.store.Len()-1, 0)
	}
	return m.selected
}

// firstMatch is where a new search lands: the nearest match at or after the
// cursor, which is the line being read. While following, the cursor is on the
// newest line and there is nothing after it, so the search goes back from
// there instead and lands on the most recent match — which in a log is the
// one that matters, where going forward would wrap round to the oldest.
func (m *Model) firstMatch() {
	m.jump(!m.follow, m.cursorLine())
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
		index, found = m.store.SearchNext(m.query, from, m.searchText)
	} else {
		index, found = m.store.SearchPrev(m.query, from, m.searchText)
	}
	if !found {
		m.notice = fmt.Sprintf("no match for `%s`", m.query)
		return
	}
	m.matchLine, m.selected = index, index
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

// nextMatchStart is the line n or N starts looking from: the one after the
// cursor or the one before it. From the cursor rather than from the last
// match, because the cursor is where the operator is: moved since, a search
// that went back to where it last stopped would jump away from what is being
// read.
func (m *Model) nextMatchStart(forward bool, lineCount int) int {
	cursor := m.cursorLine()
	if forward {
		return (cursor + 1) % lineCount
	}
	if cursor > 0 {
		return cursor - 1
	}
	return lineCount - 1
}

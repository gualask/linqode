package follow

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	if m.detail != nil {
		return m.detailView()
	}
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
	statsOn := m.statsDrawn()
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
		m.selected = max(m.store.Len()-1, 0)
	} else {
		m.selected = min(m.selected, max(m.store.Len()-1, 0))
		m.scroll = min(m.scroll, m.maxScroll())
		m.keepSelectionInView()
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
		marks := lineMarks{query: m.query, current: i == m.matchLine}
		if i == m.selected {
			fill := m.selectionStyle()
			marks.fill = &fill
		}
		lines = append(lines, renderLogLine(line, structured, marks, width))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) footer() string {
	if m.input != inputNone {
		return m.inputFooter()
	}
	if m.notice != "" {
		return m.fitFooter(theme.Yellow.Render(" " + terminalText(m.notice)))
	}
	if m.ended {
		return m.fitFooter(m.endedFooter())
	}
	return m.activeFooter()
}

// fitFooter cuts a footer to the terminal's width. A footer that wraps is two
// rows, and the frame one row taller than the screen, which scrolls the header
// off the top — panel.Footer lays the keys out not to, and what it does not
// lay out is cut here.
func (m *Model) fitFooter(line string) string {
	if m.width <= 0 {
		return line
	}
	return ansi.Truncate(line, m.width, "…")
}

// inputFooter is the prompt being typed into. It is fitted to the width by
// what it can spare: the hint goes first, then the start of what was typed,
// so the end — where the cursor is, and where the typing happens — always
// shows. `f` opens with the whole filter in it, which is easily wider than
// the hint leaves room for.
func (m *Model) inputFooter() string {
	prompt, hint := "", ""
	switch m.input {
	case inputSearch:
		prompt, hint = " /", "  enter search · esc cancel"
	case inputFilter:
		prompt, hint = " filter: ", `  key=value key!=value key="a b" · empty clears · esc cancel`
	}
	text := terminalText(m.inputText)
	if m.width > 0 {
		room := max(m.width-ansi.StringWidth(prompt)-1, 1) // the cursor's cell
		width := ansi.StringWidth(text)
		if width+ansi.StringWidth(hint) > room {
			hint = ""
		}
		if width > room {
			text = ansi.TruncateLeft(text, width-room+1, "…")
		}
	}
	return m.fitFooter(prompt + text + "▏" + theme.Dim.Render(hint))
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
	if m.statsDrawn() {
		hints = append(hints, panel.Hint{Text: "tab panels", Drop: 7})
	}
	return append(hints,
		panel.Hint{Text: "/ search", Drop: 3},
		panel.Hint{Text: "a stats", Drop: 2},
		panel.Hint{Text: "q quit", Drop: 0})
}

// focusedHints are what the log or the panel answers to. Resetting is offered
// only once there is something to reset; `f` always, since without it an
// operator who has not picked from the panel never learns the prompt exists;
// `s` is not offered at all, since detection gets it right and the key is a
// correction for when it does not.
func (m *Model) focusedHints() []panel.Hint {
	var hints []panel.Hint
	if m.statsKeys() {
		hints = []panel.Hint{{Text: "esc log", Drop: 5},
			{Text: "↑↓ move", Drop: 4}, {Text: "enter filter", Drop: 1}, {Text: "t field", Drop: 6}}
	} else {
		hints = []panel.Hint{{Text: "esc back", Drop: 1}}
		if m.store.Len() > 0 {
			hints = append(hints, panel.Hint{Text: "enter open", Drop: 3})
		}
		// Offered only once following has stopped: it is the way back, and
		// the header's `following` already says when there is nothing to
		// go back to.
		if !m.follow && m.store.Len() > 0 {
			hints = append(hints, panel.Hint{Text: "l live", Drop: 2})
		}
		if m.query != "" {
			hints = append(hints, panel.Hint{Text: "n/N next", Drop: 6})
		}
		hints = append(hints, panel.Hint{Text: "f filter", Drop: 5})
	}
	if m.store.Filter() != nil || m.query != "" {
		hints = append(hints, panel.Hint{Text: "x reset", Drop: 4})
	}
	return hints
}

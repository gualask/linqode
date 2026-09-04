package home

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

func (m *Model) View() string {
	frame := layoutFor(m.width, m.height, m.system.HasBand())

	var b strings.Builder
	b.WriteString(m.clip(m.title()) + "\n")
	if frame.band {
		b.WriteString(m.clip(m.system.Band(m.width)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.body(frame) + "\n")
	b.WriteString(m.footer())
	return b.String()
}

// clip cuts a header line to the terminal rather than trusting it to fit: a
// line one cell too wide wraps, and everything below it shifts down a row for
// as long as the reading stays wide.
func (m *Model) clip(line string) string {
	if m.width <= 0 {
		return line
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
}

func (m *Model) title() string {
	var b strings.Builder
	b.WriteString(theme.Bold.Render(" linqode "))
	b.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		b.WriteString("  ")
		b.WriteString(theme.Cyan.Render(m.info.ComposeDir))
	}
	if summary := m.services.Summary(); summary != "" {
		b.WriteString("  " + summary)
	}
	return b.String()
}

// body is the open detail, the anchor panel, or the modal that has taken the
// screen from both.
func (m *Model) body(frame frame) string {
	if m.menu != nil {
		return m.renderMenu(frame.body.width, frame.body.height)
	}
	shown, focused := m.panels[m.anchor], m.anchor == m.focus
	if m.detail != nil {
		// A detail was asked for by name; it holds focus for as long as it
		// is open.
		shown, focused = m.detail, true
	}
	content := frame.body.content()
	shown.SetSize(content.width, content.height)
	return panel.Box(shown.Title(), shown.View(), focused,
		frame.body.width, frame.body.height)
}

func (m *Model) footer() string {
	var text string
	switch {
	case m.commandPrompt:
		text = " $ " + m.commandText + "▏" + theme.Dim.Render("  enter run · esc cancel")
	case m.menu != nil:
		text = theme.Dim.Render(" j/k select · enter run · esc cancel")
	default:
		status := m.focused().Status()
		if m.detail != nil {
			status = m.detail.Status()
		}
		budget := 0
		if m.width > 0 {
			budget = m.width - lipgloss.Width(status) - len("  ·  ")
		}
		text = status + theme.Dim.Render("  ·  "+panel.JoinHints(m.hints(), budget))
	}
	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(text)
	}
	return text
}

// hints are the focused panel's keys followed by the screen's own. The Drop
// values order what is given up when the line does not fit, so the two sets
// compete on urgency rather than on which was listed first.
func (m *Model) hints() []panel.Hint {
	// Inside a detail the way out replaces the way in — the panel's own hint
	// says `enter`, which is what was just pressed. The screen's commands go
	// on working there, so they stay on the line.
	hints := []panel.Hint{{Text: "esc back", Drop: 1}}
	if m.detail == nil {
		hints = slices.Clone(m.focused().Hints())
	}
	return append(hints,
		panel.Hint{Text: "r refresh", Drop: 2},
		panel.Hint{Text: "c actions", Drop: 3},
		panel.Hint{Text: "x scripts", Drop: 6},
		panel.Hint{Text: "! run", Drop: 4},
		panel.Hint{Text: "q quit", Drop: 0})
}

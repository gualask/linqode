package home

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

func (m *Model) View() string {
	frame := layoutFor(m.width, m.height, m.services.HasHostLine())

	var b strings.Builder
	b.WriteString(m.clip(m.title()) + "\n")
	if frame.band {
		b.WriteString(m.clip(m.services.HostLine(m.width)) + "\n")
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

// body is the panels, or the modal that has taken the screen from them. The
// anchor panel holds the whole body until A3 gives the layout satellites.
func (m *Model) body(frame frame) string {
	if m.menu != nil {
		return m.renderMenu(frame.body.width, frame.body.height)
	}
	const anchor = 0
	content := frame.body.content()
	m.panels[anchor].SetSize(content.width, content.height)
	return panel.Box(m.panels[anchor].Title(), m.panels[anchor].View(),
		anchor == m.focus, frame.body.width, frame.body.height)
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
	hints := slices.Clone(m.focused().Hints())
	return append(hints,
		panel.Hint{Text: "c actions", Drop: 3},
		panel.Hint{Text: "x scripts", Drop: 6},
		panel.Hint{Text: "! run", Drop: 4},
		panel.Hint{Text: "q quit", Drop: 0})
}

package home

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

func (m *Model) View() string {
	frame := m.frame()

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

// body is the anchor panel with its satellite under it, or the detail or the
// modal that has taken the screen from both.
//
// A detail and a menu take the *whole* body, the satellite's rows included.
// Opening one changes what the screen is about, and leaving a feed running
// alongside would be showing two contexts at once — which is the thing
// panels exist to avoid.
func (m *Model) body(frame frame) string {
	whole := box{width: frame.body.width, height: frame.body.height + frame.events.height}
	if m.menu != nil {
		return m.renderMenu(whole.width, whole.height)
	}
	if m.detail != nil {
		// A detail was asked for by name; it holds focus for as long as it
		// is open.
		return m.panelBox(m.detail, whole, true)
	}
	rendered := m.panelBox(m.panels[m.anchor], frame.body, m.anchor == m.focus)
	if frame.events.height > 0 {
		rendered += "\n" + m.panelBox(m.panels[m.eventsIndex], frame.events,
			m.eventsIndex == m.focus)
	}
	return rendered
}

// panelBox sizes a panel to the space it was given and draws its chrome
// around it. The panel is told its content area, borders already subtracted,
// so no feature package has to know what a border costs.
func (m *Model) panelBox(shown panel.Panel, at box, focused bool) string {
	content := at.content()
	shown.SetSize(content.width, content.height)
	return panel.Box(shown.Title(), shown.View(), focused, at.width, at.height)
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
	// Inside a detail the way out comes first — the panel's own way *in*
	// says `enter`, which is what was just pressed — followed by whatever
	// the detail itself answers to, which is not what its header form does.
	// The screen's commands go on working there, so they stay on the line.
	hints := []panel.Hint{{Text: "esc back", Drop: 1}}
	if m.detail == nil {
		hints = slices.Clone(m.focused().Hints())
	} else {
		hints = append(hints, m.detail.Hints()...)
	}
	hints = append(hints, panel.Hint{Text: "r refresh", Drop: 2})
	// Service actions are the one screen-level command that needs compose. On
	// a host without it the key is not advertised, because everything it could
	// open is a lifecycle action on a service that was never listed. The
	// scripts and the `!` prompt stay: neither has ever needed a daemon.
	if m.services.Unavailable() == "" {
		hints = append(hints, panel.Hint{Text: "c actions", Drop: 3})
	}
	return append(hints,
		panel.Hint{Text: "x scripts", Drop: 6},
		panel.Hint{Text: "! run", Drop: 4},
		panel.Hint{Text: "q quit", Drop: 0})
}

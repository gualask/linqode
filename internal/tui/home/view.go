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
	b.WriteString(m.header(frame) + "\n")
	b.WriteString(m.body(frame) + "\n")
	b.WriteString(m.footer())
	return b.String()
}

// header is the session and the machine it is on: which host this is, and the
// row of meters saying how it is doing.
//
// It is a box, like every other region that takes focus, and it is the one box
// on this screen that costs nothing. The two rows a border needs were already
// being spent — one on the title line, one on the blank rule that kept the
// bare band off the panel below it — so the border replaces them rather than
// adding to them. That is what took the header out of a visual language of its
// own: before this, the only sign that the band held focus was its four-letter
// label changing colour, against a border and a title lighting up everywhere
// else.
//
// Without a sample there is no band to put in a box, and the session falls
// back to the plain line it always was, with the blank rule under it. Two rows
// either way, which is what the layout arithmetic already assumes.
func (m *Model) header(frame frame) string {
	if !frame.band {
		return m.clip(m.title()) + "\n"
	}
	return panel.Box(m.session(), "", m.system.Band(m.width-2), m.headerFocused(),
		m.width, headerHeight)
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

// title is the session as a bare line, for the header with no band in it. It
// keeps its own styling, which the boxed form cannot: a border title is
// rendered as one span.
func (m *Model) title() string {
	var b strings.Builder
	b.WriteString(theme.Bold.Render(" linqode "))
	b.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		b.WriteString("  ")
		b.WriteString(theme.Cyan.Render(m.info.ComposeDir))
	}
	return b.String()
}

// session is the same thing in plain text, for the header box's rule, which
// takes the focus accent across the whole label.
func (m *Model) session() string {
	text := "linqode  " + m.info.Target
	if m.info.ComposeDir != "" {
		text += "  " + m.info.ComposeDir
	}
	return text
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
// The project's service counts ride on the services panel's own rule rather
// than on the header, where they used to sit. They are about the project, the
// header is about the session and the machine, and a region that says one
// thing is read faster than one that says two. On the rule rather than in the
// footer because the footer shows only the focused panel, and what a project
// is doing is worth seeing while looking at something else.
func (m *Model) panelBox(shown panel.Panel, at box, focused bool) string {
	content := at.content()
	shown.SetSize(content.width, content.height)
	status := ""
	if shown == panel.Panel(m.services) {
		status = m.services.Summary()
	}
	return panel.Box(shown.Title(), status, shown.View(), focused, at.width, at.height)
}

func (m *Model) footer() string {
	var text string
	switch {
	case m.commandPrompt:
		text = " $ " + m.commandText + "▏" + theme.Dim.Render("  enter run · esc cancel")
	case m.menu != nil:
		text = theme.Dim.Render(" j/k select · enter run · esc cancel · q quit")
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
	// The ring is the one thing on this screen with no other way in. Every
	// other key is either on the panel that answers it or on this line, but
	// the band and the feed cannot be reached at all without knowing that
	// `tab` reaches them — and the band is where the machine's readings live.
	// First to be dropped when the line is short: it is learned once, and
	// then it is the least useful thing here.
	//
	// Not inside a detail, where `tab` moves a focus nobody can see, and not
	// on a screen with one panel, where it moves nothing at all.
	if m.detail == nil && m.drawnPanels() > 1 {
		hints = append(hints, panel.Hint{Text: "tab panels", Drop: 7})
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

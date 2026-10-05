package home

import (
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
	if !frame.headerBox {
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
	// Yellow because it is not an error and not decoration: it is the one
	// line saying the docker on this screen is not the docker on this
	// machine.
	if endpoint := m.dockerEndpoint(); endpoint != "" {
		b.WriteString("  ")
		b.WriteString(theme.Yellow.Render(endpoint))
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
	if endpoint := m.dockerEndpoint(); endpoint != "" {
		text += "  " + endpoint
	}
	return text
}

// dockerEndpoint is the header's word for a daemon somewhere else. The
// prefix is there because the value alone — `ssh://deploy@prod`, `context
// colima` — reads as a second host to connect to rather than as where this
// session's containers actually are.
func (m *Model) dockerEndpoint() string {
	if m.info.DockerEndpoint == "" {
		return ""
	}
	return "docker " + m.info.DockerEndpoint
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
	// What a panel is showing goes on its own rule. The footer is the keymap
	// and nothing else: a count of services or of events is monitoring, and
	// monitoring belongs to the region it is about, where it is legible
	// without focus and does not compete for the line where every key does.
	status := ""
	switch shown {
	case panel.Panel(m.services):
		status = m.services.Summary()
	case panel.Panel(m.events):
		status = m.events.Summary()
	}
	return panel.Box(shown.Title(), status, shown.View(), focused, at.width, at.height)
}

func (m *Model) footer() string {
	var text string
	switch {
	case m.commandPrompt:
		text = " $ " + m.commandText + "▏  " +
			panel.MarkKeys("enter run · esc cancel", true)
	case m.menu != nil:
		// A menu takes every key, so `q` is the only thing on the left that
		// still works: the split says as much rather than listing four
		// commands that would be swallowed.
		text = panel.Footer([]panel.Hint{{Text: "q quit"}}, "",
			[]panel.Hint{{Text: "↑↓ select", Drop: 2}, {Text: "enter run", Drop: 1},
				{Text: "esc cancel"}}, m.width)
	default:
		status := m.focused().Status()
		if m.detail != nil {
			status = m.detail.Status()
		}
		focused := m.focusedHints()
		if m.pending != "" {
			// Until it opens, esc gives up on it rather than going back, and
			// the line says so: it is the one way out of a link that stalled.
			status = theme.Yellow.Render(m.pending)
			focused = []panel.Hint{{Text: "esc cancel"}}
		}
		text = panel.Footer(m.globalHints(), status, focused, m.width)
	}
	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(text)
	}
	return text
}

// globalHints are the keys that work wherever you are: they sit on the left
// of the footer, the same on every screen, and are worth learning once.
func (m *Model) globalHints() []panel.Hint {
	hints := make([]panel.Hint, 0, 6)
	// The ring is the one thing here with no other way in. Every other key is
	// either on the region that answers it or already on this line, but the
	// header and the feed cannot be reached at all without knowing that `tab`
	// reaches them — and the header is where the machine's readings live.
	// First to be dropped when the line is short: learned once, and then the
	// least useful thing on it.
	//
	// Not inside a detail, where `tab` moves a focus nobody can see, and not
	// where only one panel is drawn, where it moves nothing at all.
	if m.detail == nil && m.drawnPanels() > 1 {
		hints = append(hints, panel.Hint{Text: "tab panels", Drop: 7})
	}
	hints = append(hints, panel.Hint{Text: "r refresh", Drop: 2})
	// `c actions` used to be here, and was the one key on this side whose
	// object was a selection rather than the session. It sits with the region
	// that has the selection now — see focusedHints.
	return append(hints,
		panel.Hint{Text: "x scripts", Drop: 6},
		panel.Hint{Text: "! run", Drop: 4},
		panel.Hint{Text: "q quit", Drop: 0})
}

// focusedHints are what the region with focus answers to — the half of the
// line that changes as `tab` moves.
func (m *Model) focusedHints() []panel.Hint {
	if m.detail != nil {
		// Inside a detail the way out comes first: the panel's own way *in*
		// says `enter`, which is what was just pressed.
		return append([]panel.Hint{{Text: "esc back", Drop: 1}}, m.detail.Hints()...)
	}
	hints := m.focused().Hints()
	// The action menu is the screen's, its target the panel's, so the key is
	// offered wherever there is a service under the cursor to act on — the
	// table and the feed — and nowhere else. After the keys the panel answers
	// to on its own: `enter` is what the region is for, `c` is what can then
	// be done to what it selected. Its Drop is unchanged from when it sat on
	// the other side of the rule: the side it is on moved, how badly it is
	// wanted on a narrow line did not.
	if _, offered := m.actionsHere(); offered {
		hints = append(hints, panel.Hint{Text: "c actions", Drop: 3})
	}
	return hints
}

package home

// The two ways a human asks for something to be run: a modal list of the
// commands the screen offers (service actions under `c`, configured scripts
// under `x`), and the `!` prompt for a command Linqode did not write.
//
// Both belong to the screen rather than to a panel: they take every key while
// they are open, and neither is drawn by the region it was opened from. What
// they run is another matter — a script and a typed command need nothing
// selected, while a lifecycle action is about one service, and which service
// is the focused panel's business (see actionsHere). The menu shows the exact
// command before running it, which is the other half of the keymap policy —
// related commands live behind a menu instead of behind case-variant keys
// (docs/PROJECT.md, decided policies).

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

type menuEntry struct {
	label   string
	detail  string
	request tea.Msg
}

type menu struct {
	entries  []menuEntry
	selected int
	// key is the binding that opened this menu, so pressing it again closes
	// it the way esc does.
	key string
}

// openActionMenu lists the lifecycle actions for one service. Which service
// that is has already been decided by the caller, from whichever region the
// key was pressed in.
func (m *Model) openActionMenu(service string) {
	actions := []operations.ServiceAction{
		operations.ActionRestart, operations.ActionStop, operations.ActionStart,
	}
	entries := make([]menuEntry, len(actions))
	for index, action := range actions {
		detail := action.Verb() + " " + service
		if m.info.ActionPreview != nil {
			detail = m.info.ActionPreview(action, service)
		}
		entries[index] = menuEntry{
			label: action.Verb() + " " + service, detail: detail,
			request: OpenActionMsg{
				Title: action.Verb() + ": " + service, Service: service, Action: action,
			},
		}
	}
	m.menu = &menu{entries: entries, key: "c"}
}

func (m *Model) openScriptMenu() {
	if len(m.info.Scripts) == 0 {
		m.services.SetError("no scripts configured for this host")
		return
	}
	entries := make([]menuEntry, len(m.info.Scripts))
	for index, script := range m.info.Scripts {
		entries[index] = menuEntry{
			label: script.Name, detail: script.Command,
			request: OpenScriptMsg{Title: "script: " + script.Name, Name: script.Name},
		}
	}
	m.menu = &menu{entries: entries, key: "x"}
}

func (m *Model) handleMenuKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", m.menu.key:
		// Backing out: `esc`, or the key that opened this, pressed again.
		m.menu = nil
	case "q", "ctrl+c":
		// Not a cancel. A menu takes every key while it is open, but `q`
		// means one thing on this screen and it is not "close the menu" —
		// quitting from here is safe, since nothing has been run yet.
		return tea.Quit
	case "down":
		m.menu.selected = min(m.menu.selected+1, len(m.menu.entries)-1)
	case "up":
		m.menu.selected = max(m.menu.selected-1, 0)
	case "enter":
		entry := m.menu.entries[m.menu.selected]
		m.menu = nil
		return openRequest(entry.request)
	}
	return nil
}

// menuChrome is what the menu's box costs on each axis: a border cell on
// either side, and a cell of padding inside each of those across.
const (
	menuChromeWidth  = 4
	menuChromeHeight = 2
)

// renderMenu draws the open menu as a box centred in the body, fitted to it.
//
// The rows are cut to the box rather than left to wrap, and the list scrolls
// with the selection where there are more entries than rows. What a row
// shows is the command it will run, and a script's command is whatever the
// operator wrote in the TOML — a multi-line one wrapped the box, pushed the
// frame past the bottom of the screen and turned the preview into a puzzle. It
// is drawn on one line, its breaks as spaces, the way the log view draws a
// line.
func (m *Model) renderMenu(width, height int) string {
	room, rows := 0, len(m.menu.entries)
	if width > 0 && height > 0 {
		room = max(width-menuChromeWidth, 1)
		rows = max(height-menuChromeHeight, 1)
	}
	labelWidth := 0
	for _, entry := range m.menu.entries {
		labelWidth = max(labelWidth, lipgloss.Width(panel.Plain(entry.label)))
	}
	first := max(m.menu.selected-rows+1, 0)
	last := min(first+rows, len(m.menu.entries))
	lines := make([]string, 0, last-first)
	for index := first; index < last; index++ {
		entry := m.menu.entries[index]
		label := panel.Plain(entry.label)
		head := " " + label + strings.Repeat(" ", labelWidth-lipgloss.Width(label)) + "  "
		detail := panel.Plain(entry.detail) + " "
		if room > 0 {
			head = cutMenu(head, room)
			detail = cutMenu(detail, room-lipgloss.Width(head))
		}
		if index == m.menu.selected {
			lines = append(lines, theme.Reverse.Render(head+detail))
		} else {
			lines = append(lines, theme.Bold.Render(head)+theme.Dim.Render(detail))
		}
	}
	view := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	if width > 0 && height > 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, view)
	}
	return view
}

// cutMenu cuts a menu row's part to the cells left for it.
func cutMenu(text string, room int) string {
	if room <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= room {
		return text
	}
	return ansi.Truncate(text, room, "…")
}

func (m *Model) handleCommandKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		// Every key types here except the one no terminal user expects to
		// be text: it leaves, as it does everywhere else.
		return tea.Quit
	case "esc":
		m.commandPrompt, m.commandText = false, ""
	case "enter":
		return m.submitAdHocCommand()
	case "backspace":
		if runes := []rune(m.commandText); len(runes) > 0 {
			m.commandText = string(runes[:len(runes)-1])
		}
	default:
		// What is typed — or pasted, which arrives as one key of many
		// runes — is kept as one line of text: a pasted line break would
		// otherwise be run as a second command, and an escape would be
		// drawn back at the terminal in the footer.
		if len(key.Runes) > 0 {
			m.commandText += panel.Plain(string(key.Runes))
		}
	}
	return nil
}

func (m *Model) submitAdHocCommand() tea.Cmd {
	command := strings.TrimSpace(m.commandText)
	m.commandPrompt, m.commandText = false, ""
	if command == "" {
		return nil
	}
	m.lastCommand = command
	return openRequest(OpenAdHocMsg{Title: "$ " + command, Command: command})
}

// Package home is the screen the session opens on: a header naming the
// target and the machine's vital signs, a body of focusable panels, and a
// footer saying what the keys do.
//
// It owns the screen, which the status view used to: the title line, the
// footer, the modal menus, the `!` prompt, and which region the keys are
// talking to. A feature package owns what goes inside its own panel and
// nothing beyond it, so that adding a panel is adding a panel rather than
// editing a screen.
//
// The gestures are two. `tab` moves focus between panels; `enter` descends
// one level on whichever has it — into a service's logs from the table, and
// (from A3 on) into the system view from the host band. Nothing here
// maximises a panel: opening a detail changes context, and with a single box
// on the home a layout gesture would buy nothing.
package home

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/status"
)

// Config is the session's static context, plus the previews the confirmation
// menu shows before running anything.
type Config struct {
	// Target is the `user@host` the session is connected to.
	Target string
	// ComposeDir is the remote project directory shown in the header.
	ComposeDir string
	// Scripts are the predefined commands from the config, sorted by name.
	Scripts []operations.Script
	// ActionPreview supplies the exact operations-owned command that the
	// human confirmation menu shows before running it.
	ActionPreview func(operations.ServiceAction, string) string
}

type Model struct {
	info Config

	// services is the compose table, the anchor panel. It is held by its
	// concrete type as well as through the panel ring because the screen
	// asks it questions no panel interface should carry — which service is
	// selected, what the host sample says.
	services *status.Model
	panels   []panel.Panel
	focus    int

	// menu is the open modal list, nil when none is. While one is open every
	// key routes to it instead of to the focused panel.
	menu *menu

	// commandPrompt is the `!` ad-hoc command line. While it is open every
	// key edits the text, so `q` types a q instead of quitting. commandText
	// is what has been typed; lastCommand is what was last run, which the
	// prompt reopens with — the same courtesy `f` does for log filters.
	commandPrompt bool
	commandText   string
	lastCommand   string

	width, height int
}

func New(config Config, services *status.Model) *Model {
	m := &Model{info: config, services: services}
	m.panels = []panel.Panel{services}
	m.applyFocus()
	return m
}

func (m *Model) Init() tea.Cmd { return m.services.Init() }

// focused is the panel the keys are talking to. The ring is never empty, so
// this never returns nil.
func (m *Model) focused() panel.Panel { return m.panels[m.focus] }

// applyFocus tells every panel whether it currently holds focus, so a
// selection outside the focused panel can recede instead of competing.
func (m *Model) applyFocus() {
	for index, p := range m.panels {
		p.SetFocus(index == m.focus)
	}
}

func (m *Model) moveFocus(delta int) {
	m.focus = (m.focus + delta + len(m.panels)) % len(m.panels)
	m.applyFocus()
}

// SetSize records the terminal. The panels are sized at render time instead,
// from the same layout the rendering uses, so the two can never disagree about
// how much room a panel was given.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
}

// SetError shows a failure the screen itself learned about — starting a feed,
// so far — on the panel whose data it concerns.
func (m *Model) SetError(text string) { m.services.SetError(text) }

// Refresh re-reads the service list, which the application asks for when a
// feed closes so an action's effect is visible immediately.
func (m *Model) Refresh() tea.Cmd { return m.services.Refresh() }

// UpdateBackground keeps the panels' timers and asynchronous results alive
// while another view is on screen, without starting fetches nobody can see.
func (m *Model) UpdateBackground(msg tea.Msg) (tea.Cmd, bool) {
	return m.services.UpdateBackground(msg)
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(key)
	}
	// Everything else is data: samples, ticks, stream outcomes. They belong
	// to the panel that asked for them, not to the screen.
	return m.services.Update(msg)
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.commandPrompt {
		return m.handleCommandKey(msg)
	}
	if m.menu != nil {
		return m.handleMenuKey(msg)
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return tea.Quit
	case "tab":
		m.moveFocus(1)
	case "shift+tab":
		m.moveFocus(-1)
	case "enter", "l":
		return m.open()
	case "c":
		m.openActionMenu()
	case "x":
		m.openScriptMenu()
	case "!":
		m.commandPrompt, m.commandText = true, m.lastCommand
		m.services.SetError("")
	default:
		// Anything the screen does not claim belongs to the panel with
		// focus: its own movement, its own refresh, its own modes.
		return m.focused().Update(msg)
	}
	return nil
}

// open descends one level on the focused panel. The services table opens the
// selected service's logs; the panels A3 adds answer it their own way.
func (m *Model) open() tea.Cmd {
	if m.focused() != panel.Panel(m.services) {
		return nil
	}
	service, ok := m.services.SelectedService()
	if !ok {
		return nil
	}
	return openRequest(OpenLogsMsg{Title: "logs: " + service, Service: service})
}

func openRequest(message tea.Msg) tea.Cmd {
	return func() tea.Msg { return message }
}

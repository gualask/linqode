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
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/status"
	"github.com/gualask/linqode/internal/tui/system"
)

// hostRefresh is how often the machine's meters are resampled. The command
// costs ~2 ms against the ~60 ms of the `compose ps` on the same cadence, so
// it is noise beside what is already being paid (docs/PROJECT.md, dashboard
// cost budget).
const hostRefresh = 5 * time.Second

// hostTickMsg is the meters' interval firing.
type hostTickMsg struct{}

// hostSampleMsg is one host sample, delivered asynchronously so the UI never
// blocks on the SSH round-trip.
type hostSampleMsg struct {
	metrics host.Metrics
	err     error
}

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
	// Host samples the machine's resource usage. Nil turns the band and the
	// system view off entirely — the escape hatch for a host where even a
	// cheap extra command is unwelcome.
	Host func() (host.Metrics, error)
}

type Model struct {
	info Config

	// services is the compose table, the anchor panel. It is held by its
	// concrete type as well as through the panel ring because the screen
	// asks it questions no panel interface should carry — which service is
	// selected, what the host sample says.
	services *status.Model
	// system is the machine itself: the header band, and the view an `enter`
	// on it opens.
	system *system.Model

	panels []panel.Panel
	// anchor is the panel that fills the body; focus is the one the keys are
	// talking to, which is not the same thing — the band takes focus without
	// ever leaving the header.
	anchor int
	focus  int
	// detail is the view an `enter` opened over the body, nil on the home.
	detail panel.Panel

	// hostSampling guards against a second sample while one is in flight.
	hostSampling bool

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
	m := &Model{info: config, services: services, system: system.New()}
	// Top to bottom, the way `tab` walks them. Focus starts on the table:
	// the band is what an operator reads, the table is what they act on.
	m.panels = []panel.Panel{m.system, services}
	m.anchor, m.focus = 1, 1
	m.applyFocus()
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.services.Init(), m.sampleHost(), hostTick())
}

func hostTick() tea.Cmd {
	return tea.Tick(hostRefresh, func(time.Time) tea.Msg { return hostTickMsg{} })
}

// sampleHost reads the machine's meters unless a read is already in flight,
// and is nil when no sampler is configured.
func (m *Model) sampleHost() tea.Cmd {
	if m.info.Host == nil || m.hostSampling {
		return nil
	}
	m.hostSampling = true
	fetch := m.info.Host
	return func() tea.Msg {
		metrics, err := fetch()
		return hostSampleMsg{metrics: metrics, err: err}
	}
}

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
	switch msg := msg.(type) {
	case hostTickMsg:
		return hostTick(), true
	case hostSampleMsg:
		return m.applyHostSample(msg), true
	}
	return m.services.UpdateBackground(msg)
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case hostTickMsg:
		return tea.Batch(m.sampleHost(), hostTick())
	case hostSampleMsg:
		return m.applyHostSample(msg)
	}
	// Everything else is data the table asked for: samples, ticks, stream
	// outcomes.
	return m.services.Update(msg)
}

func (m *Model) applyHostSample(msg hostSampleMsg) tea.Cmd {
	m.hostSampling = false
	m.system.SetSample(msg.metrics, msg.err)
	return nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.commandPrompt {
		return m.handleCommandKey(msg)
	}
	if m.menu != nil {
		return m.handleMenuKey(msg)
	}

	switch msg.String() {
	case "q", "esc":
		// Back out of a detail view first; from the home itself these quit,
		// which is what they do in the follow view too.
		if m.detail != nil {
			m.detail = nil
			return nil
		}
		return tea.Quit
	case "ctrl+c":
		return tea.Quit
	case "tab":
		m.moveFocus(1)
	case "shift+tab":
		m.moveFocus(-1)
	case "enter", "l":
		return m.open()
	case "r":
		// Refresh is the screen's, not a panel's: what an operator means by
		// it is "read everything again, now".
		return tea.Batch(m.services.Refresh(), m.sampleHost())
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

// open descends one level on the focused panel: from the table into the
// selected service's logs, from the band into the system view. Everything a
// later phase measures about the machine lands in that view rather than in
// another box on the home.
func (m *Model) open() tea.Cmd {
	if m.focus != m.anchor {
		if m.system.HasBand() {
			m.detail = m.system
		}
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

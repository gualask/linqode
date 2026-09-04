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

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/status"
	"github.com/gualask/linqode/internal/tui/system"
)

// The intervals the screen asks for. What it actually gets is stretched by
// the sampler when a link is slow enough to earn it.
const (
	// servicesRefresh is the cadence of `compose ps` when nothing is telling
	// the screen what changed: ~60 ms of server time per read
	// (docs/PROJECT.md, dashboard cost budget).
	servicesRefresh = 5 * time.Second
	// servicesWatched is that cadence once the daemon's event stream is up.
	// It is a safety net, not the mechanism: it catches what no event
	// describes and a stream that quietly stopped delivering, at a twelfth
	// of the round-trips.
	servicesWatched = 60 * time.Second
	// hostRefresh is the meters' cadence. At ~2 ms it is noise beside the
	// `ps` on the same clock.
	hostRefresh = 5 * time.Second
	// statsRefresh is the container readings' cadence. A sample costs ~2 s
	// on the server whatever the project's size — the daemon reads each
	// container's cgroups twice, a second apart, to derive a CPU percentage
	// — so at 20 s the command is in flight a tenth of the time. Halving it
	// would double that and say nothing new: each reading is already an
	// average over docker's own sampling second. Watching fluctuation is
	// what the live mode is for.
	statsRefresh = 20 * time.Second
)

// The samples, delivered asynchronously so the UI never blocks on an SSH
// round-trip. Each carries the reading its source asked for.
type (
	servicesSampleMsg struct {
		services []compose.Service
		err      error
	}
	hostSampleMsg struct {
		metrics host.Metrics
		err     error
	}
	statsSampleMsg struct {
		stats []compose.ContainerStats
		err   error
	}
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

	// The readings the screen samples. Each blocks on an SSH round-trip and
	// is always called from a background command, never from the UI loop.
	// A nil one is a capability this host does not offer: no fetch, no
	// cadence, and nothing on screen that would sit empty waiting for it.
	Services func() ([]compose.Service, error)
	Host     func() (host.Metrics, error)
	Stats    func() ([]compose.ContainerStats, error)

	// Watch streams the daemon's changes to this project's containers. Nil
	// leaves the service list on its timer, which is what it falls back to
	// if the stream fails or ends.
	Watch func(project string) (operations.Feed, error)
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

	// sampler owns what is read off the host and how often.
	sampler *sampler
	// project is what `ps` reported this session's containers belong to, and
	// what scopes the daemon's event stream.
	project string
	// watch is the daemon's event stream while it is up, nil otherwise.
	watch         *operations.Feed
	watchStarting bool
	// servicesStale records that something changed while a read was already
	// in flight: that read answers a question older than the news, so
	// another one follows it.
	servicesStale bool

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
	m.sampler = newSampler(map[sourceID]*source{
		sourceServices: {every: servicesRefresh, start: read(config.Services,
			func(services []compose.Service, err error) tea.Msg {
				return servicesSampleMsg{services: services, err: err}
			})},
		sourceHost: {every: hostRefresh, start: read(config.Host,
			func(metrics host.Metrics, err error) tea.Msg {
				return hostSampleMsg{metrics: metrics, err: err}
			})},
		// The live stream feeds the same columns a second at a time, so
		// while it runs the screen stops paying two seconds for a staler
		// answer.
		sourceStats: {every: statsRefresh, gate: func() bool { return !services.LiveActive() },
			start: read(config.Stats,
				func(stats []compose.ContainerStats, err error) tea.Msg {
					return statsSampleMsg{stats: stats, err: err}
				})},
	})
	// Top to bottom, the way `tab` walks them. Focus starts on the table:
	// the band is what an operator reads, the table is what they act on.
	m.panels = []panel.Panel{m.system, services}
	m.anchor, m.focus = 1, 1
	m.applyFocus()
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.sampler.due(), heartbeat())
}

// read turns a fetch into the command that carries its outcome back. A nil
// fetch yields a nil start, which is how the sampler knows a reading is not
// available on this host.
func read[T any](fetch func() (T, error), wrap func(T, error) tea.Msg) func() tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Cmd {
		return func() tea.Msg { return wrap(fetch()) }
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

// Refresh reads everything again, now: what `r` means, and what the
// application asks for when a feed closes so an action's effect is visible
// immediately. The container readings are left out — two seconds of server
// time to confirm what the table already shows is not what anyone means by
// refresh.
func (m *Model) Refresh() tea.Cmd {
	return tea.Batch(m.sampler.read(sourceServices), m.sampler.read(sourceHost))
}

// UpdateBackground keeps the panels' timers and asynchronous results alive
// while another view is on screen, without starting fetches nobody can see.
func (m *Model) UpdateBackground(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case beatMsg:
		// The heartbeat stays alive so the cadence resumes on return, but
		// nothing is read for a screen nobody is looking at.
		return heartbeat(), true
	case servicesSampleMsg, hostSampleMsg, statsSampleMsg:
		// A read already in flight when the view opened still lands.
		return m.applySample(msg), true
	case watchTickMsg:
		// The stream keeps draining: a log view is exactly when a container
		// is most likely to change, and its channel must not fill up.
		return m.handleWatchTick(), true
	case watchFeedMsg:
		return m.applyWatchFeed(msg), true
	}
	return m.services.UpdateBackground(msg)
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case beatMsg:
		return tea.Batch(m.sampler.due(), heartbeat())
	case servicesSampleMsg, hostSampleMsg, statsSampleMsg:
		return m.applySample(msg)
	case watchFeedMsg:
		return m.applyWatchFeed(msg)
	case watchTickMsg:
		return m.handleWatchTick()
	}
	// Everything else belongs to the table: its live stream, and the ticks
	// that drain it.
	return m.services.Update(msg)
}

// applySample hands one reading to whichever panel shows it, and tells the
// sampler its source is free again.
func (m *Model) applySample(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case servicesSampleMsg:
		m.services.SetServices(msg.services, msg.err)
		m.sampler.finished(sourceServices)
		if len(msg.services) > 0 {
			m.project = msg.services[0].Project
		}
		if m.servicesStale {
			// News arrived while this read was in flight, so it answers a
			// question that is already out of date.
			m.servicesStale = false
			return tea.Batch(m.startWatching(), m.sampler.read(sourceServices))
		}
		return m.startWatching()
	case hostSampleMsg:
		m.system.SetSample(msg.metrics, msg.err)
		m.sampler.finished(sourceHost)
	case statsSampleMsg:
		m.services.SetStats(msg.stats, msg.err)
		m.sampler.finished(sourceStats)
	}
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
		return m.Refresh()
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

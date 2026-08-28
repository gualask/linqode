package tui

// Compose status view: the project's services in a table, refreshed
// manually with `r` and automatically on an interval. A failed refresh
// shows its error in the footer while the last good table stays on screen.
// Enter opens the log view for the selected service.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
)

const autoRefresh = 5 * time.Second

// logTail is how many lines of history `docker compose logs` starts with.
const logTail = 200

// servicesMsg is the outcome of a refresh, delivered asynchronously so the
// UI never blocks on the SSH round-trip.
type servicesMsg struct {
	services []compose.Service
	err      error
}

type autoTickMsg struct{}

// hostMsg is one sample of the machine's resource usage, fetched alongside
// the service list on the same tick.
type hostMsg struct {
	metrics host.Metrics
	err     error
}

type statusModel struct {
	info  Info
	fetch Fetch
	// actionPreview supplies the exact operations-owned command shown by the
	// human confirmation menu.
	actionPreview func(operations.ServiceAction, string) string
	// hostFetch is optional: without it the header shows no resource line.
	hostFetch FetchHost

	// metrics is the last good host sample. A failed sample keeps it on
	// screen and flags it stale, the same way a failed service refresh
	// keeps the last good table.
	metrics           host.Metrics
	metricsLoaded     bool
	metricsStale      bool
	metricsRefreshing bool

	// statsFetch is optional: without it the table has no resource columns.
	statsFetch FetchStats
	// liveStats reports whether the backend exposes the on-demand stream.
	liveStats bool

	// stats is the latest reading per container name, keyed to match
	// compose.Service.Name. It comes from the soft sample, or from the live
	// stream while that is running.
	stats           map[string]compose.ContainerStats
	statsLoaded     bool
	statsRefreshing bool
	statsErr        string

	// statsFeed streams `docker stats` while the live panel is open; nil
	// otherwise, so the server samples nothing for a panel nobody is
	// looking at.
	statsFeed     *operations.Feed
	statsStarting bool
	// history is the CPU series per container, filled only by the live
	// stream: its samples are a second apart, which is what makes a
	// sparkline mean anything.
	history map[string][]float64

	services []compose.Service
	selected int
	// errText is the last refresh failure; the previous service list stays
	// on screen.
	errText    string
	loaded     bool // first refresh done (either way)
	refreshing bool

	// menu is the open modal list, nil when none is. While one is open,
	// keys route to it instead of to the table.
	menu *menu

	// commandPrompt is the `!` ad-hoc command line. While it is open every
	// key edits the text, so `q` types a q instead of quitting. commandText
	// is what has been typed; lastCommand is what was last run, which the
	// prompt reopens with — the same courtesy `f` does for filters.
	commandPrompt bool
	commandText   string
	lastCommand   string

	width, height int
}

func newStatusModel(info Info, fetch Fetch) statusModel {
	return statusModel{info: info, fetch: fetch}
}

// init returns the startup commands. It must not mutate state: Bubble Tea
// calls Init on a copy whose changes are discarded.
//
// The three fetches run on their own intervals — services and host metrics
// every autoRefresh, container stats every statsPollInterval — so the slow
// one never delays the cheap ones.
func (m *statusModel) init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.hostRefreshCmd(), m.statsSampleCmd(),
		autoTick(), statsPollTick())
}

func (m *statusModel) refreshCmd() tea.Cmd {
	fetch := m.fetch
	return func() tea.Msg {
		services, err := fetch()
		return servicesMsg{services: services, err: err}
	}
}

// hostRefreshCmd samples the host metrics, or nil when none are configured.
// Separate from the service fetch: it is a different command on the server
// (~2 ms against ~60 ms), and one failing must not blank the other.
func (m *statusModel) hostRefreshCmd() tea.Cmd {
	fetch := m.hostFetch
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		metrics, err := fetch()
		return hostMsg{metrics: metrics, err: err}
	}
}

// refresh starts a fetch unless one is already running.
func (m *statusModel) refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	return m.refreshCmd()
}

// refreshHost samples host metrics unless a sample is already in flight.
func (m *statusModel) refreshHost() tea.Cmd {
	if m.hostFetch == nil || m.metricsRefreshing {
		return nil
	}
	m.metricsRefreshing = true
	return m.hostRefreshCmd()
}

func autoTick() tea.Cmd {
	return tea.Tick(autoRefresh, func(time.Time) tea.Msg { return autoTickMsg{} })
}

func (m *statusModel) setSize(width, height int) {
	m.width, m.height = width, height
}

func (m *statusModel) setError(text string) {
	m.errText = text
}

func (m *statusModel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case servicesMsg:
		m.refreshing = false
		m.loaded = true
		if msg.err != nil {
			m.errText = msg.err.Error()
			return nil
		}
		// Keep the cursor on the same service across refreshes; if it is
		// gone, stay at the same position, clamped into range.
		if m.selected < len(m.services) {
			name := m.services[m.selected].Name
			for i, s := range msg.services {
				if s.Name == name {
					m.selected = i
					break
				}
			}
		}
		m.services = msg.services
		m.selected = min(m.selected, max(0, len(m.services)-1))
		m.errText = ""

	case statsSampleMsg:
		m.statsRefreshing = false
		if msg.err != nil {
			// Like a failed host sample: the columns keep their last values
			// and the table's own error reporting stays free for refresh
			// failures, which are the ones worth acting on.
			m.statsErr = msg.err.Error()
			return nil
		}
		m.statsErr = ""
		m.applySample(msg.stats)

	case statsPollMsg:
		return tea.Batch(m.refreshStats(), statsPollTick())

	case statsFeedMsg:
		m.statsStarting = false
		if msg.err != nil {
			m.statsErr = msg.err.Error()
			return nil
		}
		feed := msg.feed
		m.statsFeed = &feed
		if m.stats == nil {
			m.stats = map[string]compose.ContainerStats{}
		}
		return statsTick()

	case statsTickMsg:
		if m.statsFeed == nil {
			return nil
		}
		if ended := m.drainStats(); ended {
			// The stream stopped on its own (the project went away, or
			// docker exited). Keep the last samples on screen but stop
			// ticking for a feed that will never produce again, and let the
			// soft poll take the columns back.
			m.statsFeed.Stop()
			m.statsFeed = nil
			// The history ends with the stream: a later one would append to
			// it across a gap and draw the two as if they were continuous.
			m.history = nil
			return m.refreshStats()
		}
		return statsTick()

	case hostMsg:
		m.metricsRefreshing = false
		if msg.err != nil {
			// Keep the last sample visible, marked stale: a blip in the
			// metrics is not worth clearing the header for, and the service
			// list carries its own error reporting.
			m.metricsStale = true
			return nil
		}
		m.metrics = msg.metrics
		m.metricsLoaded = true
		m.metricsStale = false

	case autoTickMsg:
		return tea.Batch(m.refresh(), m.refreshHost(), autoTick())

	case tea.KeyMsg:
		if m.commandPrompt {
			return m.handleCommandKey(msg)
		}
		if m.menu != nil {
			return m.handleMenuKey(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "g", "home":
			m.selected = 0
		case "G", "end":
			m.selected = max(0, len(m.services)-1)
		case "r":
			return m.refresh()
		case "enter", "l":
			if m.selected < len(m.services) {
				service := m.services[m.selected].Service
				return openLogs("logs: "+service, service)
			}
		case "c":
			m.openActionMenu()
		case "x":
			m.openScriptMenu()
		case "!":
			m.commandPrompt, m.commandText = true, m.lastCommand
			m.errText = ""
		case "a":
			return m.toggleLive()
		}
	}
	return nil
}

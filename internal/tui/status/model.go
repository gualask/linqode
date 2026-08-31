package status

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

type Model struct {
	info  Config
	fetch func() ([]compose.Service, error)
	// actionPreview supplies the exact operations-owned command shown by the
	// human confirmation menu.
	actionPreview func(operations.ServiceAction, string) string
	// hostFetch is optional: without it the header shows no resource line.
	hostFetch func() (host.Metrics, error)

	// metrics is the last good host sample. A failed sample keeps it on
	// screen and flags it stale, the same way a failed service refresh
	// keeps the last good table.
	metrics           host.Metrics
	metricsLoaded     bool
	metricsStale      bool
	metricsRefreshing bool

	// statsFetch is optional: without it the table has no resource columns.
	statsFetch func() ([]compose.ContainerStats, error)
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

func New(config Config) *Model {
	return &Model{
		info:          config,
		fetch:         config.Services,
		hostFetch:     config.Host,
		statsFetch:    config.Stats,
		liveStats:     config.LiveStats,
		actionPreview: config.ActionPreview,
	}
}

// init returns the startup commands. It must not mutate state: Bubble Tea
// calls Init on a copy whose changes are discarded.
//
// The three fetches run on their own intervals — services and host metrics
// every autoRefresh, container stats every statsPollInterval — so the slow
// one never delays the cheap ones.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.hostRefreshCmd(), m.statsSampleCmd(),
		autoTick(), statsPollTick())
}

func (m *Model) refreshCmd() tea.Cmd {
	fetch := m.fetch
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		services, err := fetch()
		return servicesMsg{services: services, err: err}
	}
}

// hostRefreshCmd samples the host metrics, or nil when none are configured.
// Separate from the service fetch: it is a different command on the server
// (~2 ms against ~60 ms), and one failing must not blank the other.
func (m *Model) hostRefreshCmd() tea.Cmd {
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
func (m *Model) Refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	return m.refreshCmd()
}

// refreshHost samples host metrics unless a sample is already in flight.
func (m *Model) refreshHost() tea.Cmd {
	if m.hostFetch == nil || m.metricsRefreshing {
		return nil
	}
	m.metricsRefreshing = true
	return m.hostRefreshCmd()
}

func autoTick() tea.Cmd {
	return tea.Tick(autoRefresh, func(time.Time) tea.Msg { return autoTickMsg{} })
}

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
}

func (m *Model) SetError(text string) {
	m.errText = text
}

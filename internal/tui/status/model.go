package status

// The services panel: the compose project's services in a table, refreshed
// manually with `r` and automatically on an interval. A failed refresh
// reports itself in the panel's footer line while the last good table stays
// on screen.
//
// This package used to be the whole screen. What it owns now is the table and
// the readings behind it; the header, the footer, the modals and the keys that
// open something belong to internal/tui/home.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
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

type Model struct {
	fetch func() ([]compose.Service, error)

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

	// focused is whether the keys are currently talking to this panel. It
	// decides how the selected row is drawn, not what it does.
	focused bool

	width, height int
}

func New(config Config) *Model {
	return &Model{
		fetch:      config.Services,
		statsFetch: config.Stats,
		liveStats:  config.LiveStats,
	}
}

// init returns the startup commands. It must not mutate state: Bubble Tea
// calls Init on a copy whose changes are discarded.
//
// The two fetches run on their own intervals — the service list every
// autoRefresh, container stats every statsPollInterval — so the slow one
// never delays the cheap one.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.Sample(), autoTick(), statsPollTick())
}

// Sample runs the panel's fetches once, without arming the timers Init also
// starts. It is the seam for a caller that owns the cadence itself.
func (m *Model) Sample() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.statsSampleCmd())
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

// refresh starts a fetch unless one is already running.
func (m *Model) Refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	return m.refreshCmd()
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

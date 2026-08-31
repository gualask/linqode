package status

// Container resource usage, in the two modes the status view offers.
//
// Soft: one `docker stats --no-stream` sample every statsPollInterval,
// feeding the table's CPU and MEM columns. A sample costs ~2 s on the
// server regardless of container count — the daemon reads the cgroups
// twice, a second apart, to derive a CPU percentage — so it runs on its own
// slow interval instead of alongside the ~60 ms `compose ps`.
//
// Live: the streaming form, started on request, which emits a block per
// second into a panel below the table with a per-container sparkline. While
// it runs the soft poll stands down: the stream already delivers fresher
// numbers for the same columns.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

const (
	// statsPollInterval is how often the soft sample runs. At 20 s a ~2 s
	// command is in flight a tenth of the time; halving it would double
	// that without telling the operator anything new, since each reading is
	// still an average over docker's own sampling second. Watching
	// fluctuation is what the live mode is for.
	statsPollInterval = 20 * time.Second
	// statsDrainInterval is how often pending live samples are applied.
	// Docker emits a block per second, so this only has to be fast enough
	// to feel live.
	statsDrainInterval = 250 * time.Millisecond
	// maxStatsPerTick bounds one drain, like the log view's, so a burst
	// cannot starve input handling.
	maxStatsPerTick = 1_000
	// historyLen is how many live samples are kept per container: four
	// minutes at docker's one-per-second cadence, more than any sparkline
	// can show, so a widening terminal reveals history instead of blanks.
	historyLen = 240
	// liveMaxRows caps the live panel so it cannot crowd out the table on a
	// project with many services.
	liveMaxRows = 8
)

// statsPollMsg is the soft interval firing.
type statsPollMsg struct{}

// statsSampleMsg is the outcome of one soft sample.
type statsSampleMsg struct {
	stats []compose.ContainerStats
	err   error
}

// statsTickMsg drains the live stream.
type statsTickMsg struct{}

func statsPollTick() tea.Cmd {
	return tea.Tick(statsPollInterval, func(time.Time) tea.Msg { return statsPollMsg{} })
}

func statsTick() tea.Cmd {
	return tea.Tick(statsDrainInterval, func(time.Time) tea.Msg { return statsTickMsg{} })
}

// statsSampleCmd takes one soft sample, or nil when none is configured.
func (m *Model) statsSampleCmd() tea.Cmd {
	fetch := m.statsFetch
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		stats, err := fetch()
		return statsSampleMsg{stats: stats, err: err}
	}
}

// refreshStats starts a soft sample unless one is already in flight or the
// live stream is running — the stream feeds the same columns, faster.
func (m *Model) refreshStats() tea.Cmd {
	if m.statsFetch == nil || m.statsRefreshing || m.liveActive() {
		return nil
	}
	m.statsRefreshing = true
	return m.statsSampleCmd()
}

// applySample replaces the readings with a whole sample. Containers absent
// from it are dropped rather than kept: a service that stopped between two
// samples must not keep showing the CPU it used while running.
func (m *Model) applySample(sample []compose.ContainerStats) {
	stats := make(map[string]compose.ContainerStats, len(sample))
	for _, s := range sample {
		stats[s.Name] = s
	}
	m.stats = stats
	m.statsLoaded = true
}

// liveActive reports whether the streaming mode is on, including the window
// between asking for it and the stream arriving.
func (m *Model) liveActive() bool {
	return m.statsFeed != nil || m.statsStarting
}

// statsColumns reports whether the table carries CPU and MEM. They are part
// of the table whenever anything can fill them, and show "-" until the
// first sample lands.
func (m *Model) statsColumns() bool {
	return m.statsFetch != nil || m.liveActive() || len(m.stats) > 0
}

// toggleLive turns the streaming panel on or off. Opening pays docker's ~2 s
// of sampling latency once and then streams; closing terminates the remote
// command, so nothing is sampled on the server while the panel is hidden.
func (m *Model) toggleLive() tea.Cmd {
	if !m.liveStats {
		return nil
	}
	if m.liveActive() {
		m.stopLive()
		// The soft poll owns the columns again. The numbers just off the
		// stream are a second old, so the next scheduled sample is soon
		// enough — no need to spend 2 s of server time right now.
		return nil
	}
	m.statsStarting = true
	m.statsErr = ""
	return func() tea.Msg { return OpenStatsMsg{} }
}

// stopLive tears the stream down and forgets its history. The readings
// themselves survive only if the soft poll is there to refresh them;
// otherwise they would freeze on screen, and stale numbers read as live.
func (m *Model) stopLive() {
	if m.statsFeed != nil {
		m.statsFeed.Stop()
		m.statsFeed = nil
	}
	m.statsStarting = false
	m.history = nil
	m.statsErr = ""
	if m.statsFetch == nil {
		m.stats = nil
		m.statsLoaded = false
	}
}

// drainStats applies pending live samples, reporting whether the stream
// ended.
func (m *Model) drainStats() bool {
	for range maxStatsPerTick {
		select {
		case event, ok := <-m.statsFeed.Events:
			if !ok {
				return true
			}
			switch event.Kind {
			case operations.EventStats:
				m.stats[event.Stats.Name] = event.Stats
				m.statsLoaded = true
				m.record(event.Stats)
			case operations.EventStderr:
				// docker writes its complaints here; the last one is the
				// useful one.
				m.statsErr = event.Text
			case operations.EventExit:
				return true
			}
		default:
			return false
		}
	}
	return false
}

// record appends a container's CPU reading to its history, dropping the
// oldest once the window is full.
func (m *Model) record(stats compose.ContainerStats) {
	percent, ok := stats.CPUPercent()
	if !ok {
		return
	}
	if m.history == nil {
		m.history = map[string][]float64{}
	}
	series := append(m.history[stats.Name], percent)
	if len(series) > historyLen {
		series = series[len(series)-historyLen:]
	}
	m.history[stats.Name] = series
}

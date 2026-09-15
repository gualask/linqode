package status

// Container resource usage, in the two modes the status view offers.
//
// Sampled: the screen reads each container's cgroup counters on its own
// cadence (internal/tui/home) and hands every derived sample to SetStats,
// which fills the table's CPU, MEM, NET and IO columns.
//
// Live: `docker stats` in its streaming form, started on request, a block a
// second into a panel below the table. While it runs the sampled source
// stands down: the stream already delivers fresher numbers for the same
// columns.
//
// Both feed one trend per container, which is what the live panel draws.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

const (
	// statsDrainInterval is how often pending live samples are applied.
	// Docker emits a block per second, so this only has to be fast enough
	// to feel live.
	statsDrainInterval = 250 * time.Millisecond
	// maxStatsPerTick bounds one drain, like the log view's, so a burst
	// cannot starve input handling.
	maxStatsPerTick = 1_000
	// trendAge is how far back a container's trend reaches: ten minutes, the
	// same as the machine's, which is long enough for a leak to read as a
	// climb.
	trendAge = 10 * time.Minute
	// trendDepth bounds it by count as well, for the stream's one sample a
	// second: ten minutes of those, more than any strip draws, so a widening
	// terminal reveals history instead of blanks.
	trendDepth = 600
	// liveMaxRows caps the live panel so it cannot crowd out the table on a
	// project with many services.
	liveMaxRows = 8
)

// statsTickMsg drains the live stream.
type statsTickMsg struct{}

func statsTick() tea.Cmd {
	return tea.Tick(statsDrainInterval, func(time.Time) tea.Msg { return statsTickMsg{} })
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
	for _, s := range sample {
		m.record(s)
	}
}

// liveActive reports whether the streaming mode is on, including the window
// between asking for it and the stream arriving.
func (m *Model) liveActive() bool {
	return m.statsFeed != nil || m.statsStarting
}

// statsColumns reports whether the table carries the per-container readings
// — CPU, MEM, NET and BLOCK. They are part of the table whenever anything
// can fill them, and show "-" until the first sample lands.
func (m *Model) statsColumns() bool {
	return m.softStats || m.liveActive() || len(m.stats) > 0
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
	m.statsRequest++
	m.statsErr = ""
	requestID := m.statsRequest
	return func() tea.Msg { return OpenStatsMsg{RequestID: requestID} }
}

// stopLive tears the stream down. The readings and their trends survive only
// if the sampled source is there to go on filling them; otherwise they would
// freeze, and stale numbers read as live.
func (m *Model) stopLive() {
	if m.statsFeed != nil {
		m.statsFeed.Stop()
		m.statsFeed = nil
	}
	m.statsStarting = false
	m.statsErr = ""
	if !m.softStats {
		m.stats = nil
		m.statsLoaded = false
		m.trends = nil
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

// point is one moment of a container's trend. Either reading can be
// missing: the first cgroup sample has no CPU percentage, which needs two,
// and a container being torn down reports neither.
type point struct {
	at            time.Time
	cpu           float64
	hasCPU        bool
	memory        float64 // bytes
	memoryPercent float64
	hasMemory     bool
}

// record appends a container's readings to its trend, dropping what is older
// than trendAge or beyond trendDepth.
func (m *Model) record(stats compose.ContainerStats) {
	sample := point{at: m.now()}
	sample.cpu, sample.hasCPU = stats.CPUPercent()
	if bytes, ok := stats.MemBytes(); ok {
		sample.memory, sample.hasMemory = float64(bytes), true
		sample.memoryPercent, _ = stats.MemPercent()
	}
	if !sample.hasCPU && !sample.hasMemory {
		return
	}
	if m.trends == nil {
		m.trends = map[string][]point{}
	}
	series := append(m.trends[stats.Name], sample)
	cutoff := sample.at.Add(-trendAge)
	first := 0
	for first < len(series)-1 && series[first].at.Before(cutoff) {
		first++
	}
	first = max(first, len(series)-trendDepth)
	m.trends[stats.Name] = series[first:]
}

// forgetDeparted drops the trends of containers the service list no longer
// names. A recreated container keeps its name and therefore its trend, which
// is right: the service is the thing being watched, not the container id.
func (m *Model) forgetDeparted() {
	names := make(map[string]bool, len(m.services))
	for _, service := range m.services {
		names[service.Name] = true
	}
	for name := range m.trends {
		if !names[name] {
			delete(m.trends, name)
		}
	}
}

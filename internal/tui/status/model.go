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
	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

type Model struct {
	// softStats reports whether the screen samples container readings for
	// this host; without it the table has no resource columns unless the
	// live stream is filling them.
	softStats bool
	// liveStats reports whether the backend exposes the on-demand stream.
	liveStats bool
	// unavailable is why there is no compose on this host, established once
	// at connect. Every compose affordance is off while it is set.
	unavailable string

	// stats is the latest reading per container name, keyed to match
	// compose.Service.Name. It comes from the screen's sample, or from the
	// live stream while that is running.
	stats       map[string]compose.ContainerStats
	statsLoaded bool
	statsErr    string

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
	errText string
	loaded  bool // first sample applied (either way)

	// focused is whether the keys are currently talking to this panel. It
	// decides how the selected row is drawn, not what it does.
	focused bool

	width, height int
}

func New(config Config) *Model {
	return &Model{
		softStats: config.Stats, liveStats: config.LiveStats,
		unavailable: config.Unavailable,
	}
}

// Unavailable is why this panel has no table, empty when it has one.
func (m *Model) Unavailable() string { return m.unavailable }

// SetServices applies one reading of the service list. A failure keeps the
// last good table on screen and reports itself in the panel's footer line:
// a table a few seconds old says more than an empty one.
func (m *Model) SetServices(services []compose.Service, err error) {
	m.loaded = true
	if err != nil {
		m.errText = err.Error()
		return
	}
	// Keep the cursor on the same service across refreshes; if it is gone,
	// stay at the same position, clamped into range.
	m.selected = selectedServiceIndex(m.selected, m.services, services)
	m.services = services
	m.selected = min(m.selected, max(0, len(m.services)-1))
	m.errText = ""
}

// SetStats applies one reading of the container resources. A whole sample
// replaces the previous one, so a container that stopped between two samples
// loses its numbers rather than freezing them. A failed sample keeps the
// readings and reports itself, as a failed host sample keeps the meters.
func (m *Model) SetStats(stats []compose.ContainerStats, err error) {
	if err != nil {
		m.statsErr = err.Error()
		return
	}
	m.statsErr = ""
	m.applySample(stats)
}

// LiveActive reports whether the streaming mode is on, which is the screen's
// cue to stand its own sampling down: the stream feeds the same columns,
// faster.
func (m *Model) LiveActive() bool { return m.liveActive() }

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
}

func (m *Model) SetError(text string) {
	m.errText = text
}

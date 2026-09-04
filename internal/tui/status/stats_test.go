package status

// Tests for the two resource modes: the periodic soft sample behind the
// table's CPU and MEM columns, and the live stream behind the panel below
// it — including that the two never run at once.

import (
	"errors"
	"strings"
	"testing"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

// sample builds one soft sample from readings.
func sample(readings ...compose.ContainerStats) []compose.ContainerStats {
	return readings
}

// reading is one container's numbers, named to match services().
func reading(service, cpu, mem string) compose.ContainerStats {
	return compose.ContainerStats{
		Name:     "app-" + service + "-1",
		CPUPerc:  cpu,
		MemUsage: mem + " / 2GiB",
		MemPerc:  "7.50%",
		NetIO:    "1.2kB / 640B",
		BlockIO:  "0B / 8.19kB",
	}
}

func liveReading(service, cpu, mem string) operations.Event {
	return operations.Event{Kind: operations.EventStats, Stats: reading(service, cpu, mem)}
}

// withStatsFetch returns a model with the soft sample enabled. The function
// itself is never called: these tests deliver samples directly.
func withStatsFetch() *Model {
	m := New(Config{})
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.liveStats = true
	m.SetSize(120, 24)
	return m
}

// statsStream is a hand-built feed standing in for the remote command.
type statsStream struct {
	events  chan operations.Event
	stopped bool
}

func newStatsStream() *statsStream {
	return &statsStream{events: make(chan operations.Event, 16)}
}

func (s *statsStream) feed() operations.Feed {
	return operations.Feed{Events: s.events, Stop: func() { s.stopped = true }}
}

// openLive drives the model through the toggle → feed handshake.
func openLive(t *testing.T, m *Model) *statsStream {
	t.Helper()
	cmd := m.Update(key("a"))
	if cmd == nil {
		t.Fatal("`a` did not start the live stream")
	}
	_, ok := cmd().(OpenStatsMsg)
	if !ok {
		t.Fatalf("`a` produced %T, want OpenStatsMsg", cmd())
	}
	stream := newStatsStream()
	m.Update(StatsFeedMsg{Feed: stream.feed()})
	return stream
}

// The columns are part of the table from the start when something can fill
// them: a service with no reading yet shows a placeholder, rather than the
// table growing a column later.
func TestStatsColumnsPresentBeforeFirstSample(t *testing.T) {
	m := withStatsFetch()
	m.Update(servicesMsg{services: services("web", "db")})

	view := m.View()
	for _, header := range []string{"CPU", "MEM", "NET RX/TX", "IO R/W"} {
		if !strings.Contains(view, header) {
			t.Errorf("resource column %s missing before the first sample:\n%s", header, view)
		}
	}

	// The I/O pairs are the first thing given up when the terminal is the
	// constraint: ellipsized, "1.45GB/8…" has lost the half that made it a
	// pair, while CPU and MEM stay readable at any width.
	m.SetSize(ioColumnsMinWidth-1, 24)
	narrow := m.View()
	for _, header := range []string{"NET RX/TX", "IO R/W"} {
		if strings.Contains(narrow, header) {
			t.Errorf("%s kept below %d columns:\n%s", header, ioColumnsMinWidth, narrow)
		}
	}
	for _, header := range []string{"CPU", "MEM"} {
		if !strings.Contains(narrow, header) {
			t.Errorf("%s dropped along with the I/O columns:\n%s", header, narrow)
		}
	}

	// Without a fetch configured there is nothing to fill them, and they
	// stay out of the table entirely.
	off := New(Config{})
	off.SetSize(120, 24)
	off.Update(servicesMsg{services: services("web")})
	if strings.Contains(off.View(), "CPU") {
		t.Errorf("resource columns present with no way to fill them:\n%s", off.View())
	}
}

func TestSoftSampleFillsColumns(t *testing.T) {
	m := withStatsFetch()
	m.Update(servicesMsg{services: services("web", "db")})
	m.Update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	view := m.View()
	if !strings.Contains(view, "12.34%") {
		t.Errorf("CPU reading not shown:\n%s", view)
	}
	// Only the used half of "153.6MiB / 2GiB": the limit repeats on every row.
	if !strings.Contains(view, "153.6MiB") {
		t.Errorf("memory reading not shown:\n%s", view)
	}
	if strings.Contains(view, "2GiB") {
		t.Errorf("memory limit should not take table width:\n%s", view)
	}
	// The I/O pairs keep both halves, with docker's spaces squeezed out so
	// they cannot be mistaken for the table's own column gap.
	if !strings.Contains(view, "1.2kB/640B") {
		t.Errorf("network reading not shown:\n%s", view)
	}
	if !strings.Contains(view, "0B/8.19kB") {
		t.Errorf("block I/O reading not shown:\n%s", view)
	}
	// db has no reading yet, and must still render a row.
	if !strings.Contains(view, "db") {
		t.Errorf("unsampled service dropped from the table:\n%s", view)
	}
}

// A whole sample replaces the previous one: a container missing from it has
// stopped, and its last numbers must not linger as if they were current.
func TestSoftSampleReplacesPreviousReadings(t *testing.T) {
	m := withStatsFetch()
	m.Update(servicesMsg{services: services("web", "db")})
	m.Update(statsSampleMsg{stats: sample(
		reading("web", "12.34%", "153.6MiB"),
		reading("db", "3.20%", "64MiB"),
	)})
	m.Update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	if strings.Contains(m.View(), "3.20%") {
		t.Errorf("reading of a vanished container survived the next sample:\n%s", m.View())
	}
}

// A failed sample keeps the last readings, like a failed host sample keeps
// the header: a blip is not worth blanking the columns for.
func TestFailedSoftSampleKeepsReadings(t *testing.T) {
	m := withStatsFetch()
	m.Update(servicesMsg{services: services("web")})
	m.Update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})
	m.Update(statsSampleMsg{err: errors.New("cannot connect to the docker daemon")})

	view := m.View()
	if !strings.Contains(view, "12.34%") {
		t.Errorf("readings dropped after a failed sample:\n%s", view)
	}
	if !strings.Contains(m.Status(), "cannot connect") {
		t.Errorf("sample failure not reported: %q", m.Status())
	}

	m.Update(statsSampleMsg{stats: sample(reading("web", "1.00%", "150MiB"))})
	if strings.Contains(m.Status(), "cannot connect") {
		t.Error("error survived a good sample")
	}
}

func TestSoftSampleDoesNotOverlap(t *testing.T) {
	m := withStatsFetch()
	if cmd := m.refreshStats(); cmd == nil {
		t.Fatal("first sample did not start")
	}
	if cmd := m.refreshStats(); cmd != nil {
		t.Error("second sample started while one was in flight")
	}
	m.Update(statsSampleMsg{stats: nil})
	if cmd := m.refreshStats(); cmd == nil {
		t.Error("sampling did not resume after the previous one landed")
	}
}

func TestStatsPollTickKeepsTicking(t *testing.T) {
	m := withStatsFetch()
	if cmd := m.Update(statsPollMsg{}); cmd == nil {
		t.Error("the poll tick did not schedule the next one")
	}
	// With no fetch configured the tick still reschedules; only the sample
	// is skipped.
	off := New(Config{})
	if cmd := off.Update(statsPollMsg{}); cmd == nil {
		t.Error("the poll tick stopped when no sample is configured")
	}
}

package status

// Tests for the status header's host resource line: when it appears, what
// it drops, and how it survives a failed sample.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/theme"
)

func sampleMetrics() host.Metrics {
	return host.Metrics{
		Load1: 0.50, Load5: 0.40, Load15: 0.30,
		CPUs:           4,
		Uptime:         90 * time.Minute,
		MemTotalKB:     2048000,
		MemAvailableKB: 1024000,
		DiskTotalKB:    20000000,
		DiskUsedKB:     5000000,
	}
}

// withHostFetch returns a model that has host metrics enabled; the function
// itself is never called by these tests, which deliver samples directly.
func withHostFetch() *Model {
	m := New(Config{})
	m.hostFetch = func() (host.Metrics, error) { return host.Metrics{}, nil }
	return m
}

func TestHostLineAppearsOnlyAfterFirstSample(t *testing.T) {
	m := withHostFetch()
	// "/cpu" rather than "load": the empty-table placeholder says "loading".
	if strings.Contains(m.View(), "/cpu") {
		t.Errorf("resource line shown before any sample arrived:\n%s", m.View())
	}

	m.Update(hostMsg{metrics: sampleMetrics()})

	view := m.View()
	for _, want := range []string{"load 0.50", "0.12/cpu", "mem", "disk", "up 1h30m"} {
		if !strings.Contains(view, want) {
			t.Errorf("resource line missing %q:\n%s", want, view)
		}
	}
}

func TestNoHostLineWithoutFetch(t *testing.T) {
	m := New(Config{})
	// Even if a sample somehow arrived, nothing is configured to produce
	// one, and the header stays as it was.
	if line := m.renderHostLine(); line != "" {
		t.Errorf("resource line rendered without a host fetch: %q", line)
	}
	if cmd := m.refreshHost(); cmd != nil {
		t.Error("refreshHost started a command with no host fetch configured")
	}
}

// A metrics blip must not clear the header: the last good sample stays,
// flagged, exactly as a failed service refresh keeps the last good table.
func TestFailedHostSampleKeepsLastAndMarksStale(t *testing.T) {
	m := withHostFetch()
	m.Update(hostMsg{metrics: sampleMetrics()})
	m.Update(hostMsg{err: errors.New("connection lost")})

	view := m.View()
	if !strings.Contains(view, "load 0.50") {
		t.Errorf("previous sample dropped after a failure:\n%s", view)
	}
	if !strings.Contains(view, "stale") {
		t.Errorf("stale sample not flagged:\n%s", view)
	}
	// The metrics error belongs in the header, not in the footer where
	// service errors live.
	if strings.Contains(view, "connection lost") {
		t.Errorf("host metrics error leaked into the view:\n%s", view)
	}

	m.Update(hostMsg{metrics: sampleMetrics()})
	if strings.Contains(m.View(), "stale") {
		t.Errorf("stale flag survived a good sample:\n%s", m.View())
	}
}

// A kernel that does not report one of the sources still yields a useful
// line from the rest.
func TestHostLineDropsUnreportedParts(t *testing.T) {
	m := withHostFetch()
	m.Update(hostMsg{metrics: host.Metrics{Load1: 1.5, CPUs: 2}})

	line := m.renderHostLine()
	if !strings.Contains(line, "load 1.50") {
		t.Errorf("load missing from line: %q", line)
	}
	for _, absent := range []string{"mem", "disk", "up "} {
		if strings.Contains(line, absent) {
			t.Errorf("line reports %q it never received: %q", absent, line)
		}
	}
}

// An in-flight sample must not be duplicated by the next tick.
func TestHostRefreshDoesNotOverlap(t *testing.T) {
	m := withHostFetch()
	if cmd := m.refreshHost(); cmd == nil {
		t.Fatal("first refresh did not start")
	}
	if cmd := m.refreshHost(); cmd != nil {
		t.Error("second refresh started while one was in flight")
	}
	m.Update(hostMsg{metrics: sampleMetrics()})
	if cmd := m.refreshHost(); cmd == nil {
		t.Error("refresh did not resume after the sample arrived")
	}
}

func TestFormatKB(t *testing.T) {
	cases := []struct {
		kb   uint64
		want string
	}{
		{0, "0.0K"},
		{512, "512K"},
		{1024, "1.0M"},
		{2048000, "2.0G"},
		{20000000, "19.1G"},
	}
	for _, c := range cases {
		if got := formatKB(c.kb); got != c.want {
			t.Errorf("formatKB(%d) = %q, want %q", c.kb, got, c.want)
		}
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Minute, "45m"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "1d1h"},
		{72 * time.Hour, "3d0h"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// The thresholds are what make the line scannable; pin them so a refactor
// cannot quietly turn every value green.
func TestUsageAndLoadThresholds(t *testing.T) {
	if usageStyle(50).GetForeground() != theme.Green.GetForeground() {
		t.Error("50% should render green")
	}
	if usageStyle(80).GetForeground() != theme.Yellow.GetForeground() {
		t.Error("80% should render yellow")
	}
	if usageStyle(95).GetForeground() != theme.Red.GetForeground() {
		t.Error("95% should render red")
	}
	if loadStyle(0.5).GetForeground() != theme.Green.GetForeground() {
		t.Error("half-busy load should render green")
	}
	if loadStyle(1.2).GetForeground() != theme.Red.GetForeground() {
		t.Error("load above one per core should render red")
	}
}

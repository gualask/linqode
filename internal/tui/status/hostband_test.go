package status

// Tests for the header's meter band and the project summary beside the
// target, and for the invariant both of them exist to protect: the view
// never renders a line wider than the terminal.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
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

// The bars share whatever the labels and amounts leave, so a wider terminal
// buys resolution rather than padding.
func TestMeterBarsStretchWithTheTerminal(t *testing.T) {
	m := withHostFetch()
	m.Update(hostMsg{metrics: sampleMetrics()})

	m.SetSize(80, 24)
	narrow := strings.Count(m.renderHostLine(m.width), "░") + strings.Count(m.renderHostLine(m.width), "█")
	m.SetSize(160, 24)
	wide := strings.Count(m.renderHostLine(m.width), "░") + strings.Count(m.renderHostLine(m.width), "█")

	if wide <= narrow {
		t.Errorf("bars did not grow with the terminal: %d cells at 80, %d at 160", narrow, wide)
	}
	if narrow < 3*meterMinBar {
		t.Errorf("bars fell below the floor at 80 columns: %d cells", narrow)
	}
}

// The band must never be the thing that wraps the header.
func TestMeterBandFitsItsWidth(t *testing.T) {
	m := withHostFetch()
	m.Update(hostMsg{metrics: sampleMetrics()})
	for _, width := range []int{80, 100, 130, 200} {
		m.SetSize(width, 24)
		if got := lipgloss.Width(m.renderHostLine(m.width)); got > width {
			t.Errorf("band is %d wide at %d columns", got, width)
		}
	}
}

func TestNoHostLineWithoutFetch(t *testing.T) {
	m := New(Config{})
	// Even if a sample somehow arrived, nothing is configured to produce
	// one, and the header stays as it was.
	if line := m.renderHostLine(m.width); line != "" {
		t.Errorf("resource line rendered without a host fetch: %q", line)
	}
	if cmd := m.refreshHost(); cmd != nil {
		t.Error("refreshHost started a command with no host fetch configured")
	}
}

// A kernel that does not report one of the sources still yields a useful
// line from the rest.
func TestHostLineDropsUnreportedParts(t *testing.T) {
	m := withHostFetch()
	m.Update(hostMsg{metrics: host.Metrics{Load1: 1.5, CPUs: 2}})

	line := m.renderHostLine(m.width)
	if !strings.Contains(line, "load[") || !strings.Contains(line, "1.50") {
		t.Errorf("load missing from band: %q", line)
	}
	for _, absent := range []string{"mem", "disk", "up "} {
		if strings.Contains(line, absent) {
			t.Errorf("band reports %q it never received: %q", absent, line)
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

// The table owns the full width now that nothing sits beside it.
func TestTableKeepsItsColumnsAtFullWidth(t *testing.T) {
	m := withHostFetch()
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.SetSize(130, 24)
	m.Update(servicesMsg{services: services("web", "db")})
	m.Update(hostMsg{metrics: sampleMetrics()})
	m.Update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	view := m.View()
	for _, want := range []string{"SERVICE", "STATE", "HEALTH", "CPU", "MEM", "NET RX/TX", "IO R/W", "STATUS"} {
		if !strings.Contains(view, want) {
			t.Errorf("column %q missing:\n%s", want, view)
		}
	}
}

// Numbers are compared by scanning down a column, which only works when
// their digits line up. The live panel already prints its readings this
// way; the table used to disagree with the panel beside it.
func TestNumericColumnsAlignRight(t *testing.T) {
	m := withHostFetch()
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.SetSize(140, 24)
	m.Update(servicesMsg{services: services("web", "db")})
	m.Update(statsSampleMsg{stats: sample(
		reading("web", "112.34%", "1.234GiB"),
		reading("db", "3.02%", "64MiB"),
	)})

	// The short readings are pushed right so their last characters sit
	// under the long ones', rather than every cell starting at its column's
	// left edge.
	view := m.View()
	long := strings.Index(view, "112.34%") + len("112.34%")
	short := strings.Index(view, "3.02%") + len("3.02%")
	lineStart := func(at int) int { return strings.LastIndex(view[:at], "\n") + 1 }
	if long-lineStart(long) != short-lineStart(short) {
		t.Errorf("CPU readings do not end in the same column:\n%s", view)
	}
	longMem := strings.Index(view, "1.234GiB") + len("1.234GiB")
	shortMem := strings.Index(view, "64MiB") + len("64MiB")
	if longMem-lineStart(longMem) != shortMem-lineStart(shortMem) {
		t.Errorf("memory readings do not end in the same column:\n%s", view)
	}
}

// The table's heading is a band across the whole terminal: a table narrower
// than the screen must not look like it was cut off part way.
func TestTableHeadingSpansTheWidth(t *testing.T) {
	m := withHostFetch()
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.SetSize(160, 24)
	m.Update(servicesMsg{services: services("web")})
	m.Update(hostMsg{metrics: sampleMetrics()})

	var heading string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "SERVICE") {
			heading = line
			break
		}
	}
	if heading == "" {
		t.Fatalf("no heading row in:\n%s", m.View())
	}
	if got := lipgloss.Width(heading); got != 160 {
		t.Errorf("heading spans %d of 160 columns: %q", got, heading)
	}
}

// The gap widens when the terminal can afford it and narrows when the
// alternative is ellipsizing content. Breathing room is worth having, but
// not at the price of the readings it separates.
func TestColumnGapAdaptsToWidth(t *testing.T) {
	m := withHostFetch()
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.Update(servicesMsg{services: services("web", "db")})
	m.Update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	headers := m.tableHeaders()
	rows := [][]cell{m.serviceRow(m.services[0]), m.serviceRow(m.services[1])}

	_, wide := columnLayout(headers, rows, 200)
	if wide != columnGaps[0] {
		t.Errorf("gap at 200 columns is %d, want the widest %d", wide, columnGaps[0])
	}
	_, tight := columnLayout(headers, rows, 60)
	if tight != columnGaps[len(columnGaps)-1] {
		t.Errorf("gap at 60 columns is %d, want the narrowest %d",
			tight, columnGaps[len(columnGaps)-1])
	}

	// Whatever gap it picks, the row must still fit.
	for _, width := range []int{60, 80, 100, 120, 140, 200} {
		widths, gap := columnLayout(headers, rows, width)
		total := 1 + gap*(len(widths)-1)
		for _, columnWidth := range widths {
			total += columnWidth
		}
		if total != width {
			t.Errorf("at %d columns the row totals %d (gap %d)", width, total, gap)
		}
	}
}

func TestBarClamps(t *testing.T) {
	if got := bar(0, 4); got != "░░░░" {
		t.Errorf("bar(0) = %q", got)
	}
	if got := bar(100, 4); got != "████" {
		t.Errorf("bar(100) = %q", got)
	}
	// Load can exceed one per core; the bar fills rather than overflowing.
	if got := bar(250, 4); got != "████" {
		t.Errorf("bar(250) = %q", got)
	}
	if got := bar(50, 4); got != "██░░" {
		t.Errorf("bar(50) = %q", got)
	}
	if got := bar(50, 0); got != "" {
		t.Errorf("bar with no width = %q", got)
	}
}

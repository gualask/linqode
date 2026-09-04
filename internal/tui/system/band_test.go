package system

// Tests for the meter band: what it drops when the terminal narrows, what it
// leaves out when the kernel does not report it, and the thresholds that make
// it scannable. The screen-level questions — whether the band is on the header
// at all, whether it went stale — live with the screen, in internal/tui/home.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

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

// sampled is a model holding one good sample, which is the state every one of
// these tests starts from.
func sampled(metrics host.Metrics) *Model {
	m := New()
	m.SetSample(metrics, nil)
	return m
}

// The bars share whatever the labels and amounts leave, so a wider terminal
// buys resolution rather than padding.
func TestMeterBarsStretchWithTheTerminal(t *testing.T) {
	m := sampled(sampleMetrics())

	cells := func(width int) int {
		return strings.Count(m.Band(width), "░") + strings.Count(m.Band(width), "█")
	}
	narrow, wide := cells(80), cells(160)

	if wide <= narrow {
		t.Errorf("bars did not grow with the terminal: %d cells at 80, %d at 160", narrow, wide)
	}
	if narrow < 3*meterMinBar {
		t.Errorf("bars fell below the floor at 80 columns: %d cells", narrow)
	}
}

// The band must never be the thing that wraps the header.
func TestMeterBandFitsItsWidth(t *testing.T) {
	m := sampled(sampleMetrics())
	for _, width := range []int{80, 100, 130, 200} {
		if got := lipgloss.Width(m.Band(width)); got > width {
			t.Errorf("band is %d wide at %d columns", got, width)
		}
	}
}

// Before the first sample there is nothing to draw, and the header must not
// reserve a row for numbers that are not there.
func TestNoBandBeforeTheFirstSample(t *testing.T) {
	m := New()
	if m.HasBand() {
		t.Error("band claimed a row before any sample arrived")
	}
	if line := m.Band(120); line != "" {
		t.Errorf("band rendered without a sample: %q", line)
	}
}

// A kernel that does not report one of the sources still yields a useful
// line from the rest.
func TestHostLineDropsUnreportedParts(t *testing.T) {
	m := sampled(host.Metrics{Load1: 1.5, CPUs: 2})

	line := m.Band(120)
	if !strings.Contains(line, "load[") || !strings.Contains(line, "1.50") {
		t.Errorf("load missing from band: %q", line)
	}
	for _, absent := range []string{"mem", "disk", "up "} {
		if strings.Contains(line, absent) {
			t.Errorf("band reports %q it never received: %q", absent, line)
		}
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
	if theme.Usage(50).GetForeground() != theme.Green.GetForeground() {
		t.Error("50% should render green")
	}
	if theme.Usage(80).GetForeground() != theme.Yellow.GetForeground() {
		t.Error("80% should render yellow")
	}
	if theme.Usage(95).GetForeground() != theme.Red.GetForeground() {
		t.Error("95% should render red")
	}
	if loadStyle(0.5).GetForeground() != theme.Green.GetForeground() {
		t.Error("half-busy load should render green")
	}
	if loadStyle(1.2).GetForeground() != theme.Red.GetForeground() {
		t.Error("load above one per core should render red")
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

package home

// Tests for the screen as a whole: the header block, the meter band's place
// in it, the project summary on the title line, and the invariant all of them
// exist to protect — no line is ever wider than the terminal, because one that
// is wraps and shifts everything below it down a row.
//
// They came from the status package with the screen itself. What stayed there
// is what the panel still owns: how a meter band is drawn at a given width,
// how the table lays out its columns.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/status"
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

// hostFeed is a host sampler whose answer the test can change between
// samples, which is how a stale reading is reached: through the panel's own
// failure path rather than by writing its fields.
type hostFeed struct {
	metrics host.Metrics
	err     error
}

func (h *hostFeed) sample() (host.Metrics, error) {
	if h.err != nil {
		return host.Metrics{}, h.err
	}
	return h.metrics, nil
}

type screenOptions struct {
	width, height int
	services      []compose.Service
	host          *hostFeed
	stats         []compose.ContainerStats
}

func buildScreen(options screenOptions) (*Model, *status.Model) {
	config := status.Config{
		Services: func() ([]compose.Service, error) { return options.services, nil },
	}
	if options.stats != nil {
		config.Stats = func() ([]compose.ContainerStats, error) { return options.stats, nil }
	}
	panel := status.New(config)
	screenConfig := Config{Target: "deploy@prod"}
	if options.host != nil {
		screenConfig.Host = options.host.sample
	}
	screen := New(screenConfig, panel)
	screen.SetSize(options.width, options.height)
	return screen, panel
}

// sampleAll fills both the table and the band, the way the first tick does.
func sampleAll(screen *Model, panel *status.Model) {
	apply(panel, panel.Sample())
	applyScreen(screen, screen.sampleHost())
}

// applyScreen runs a screen-owned command and feeds its message back, the way
// the Bubble Tea loop would.
func applyScreen(screen *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, sub := range msg {
			applyScreen(screen, sub)
		}
	default:
		applyScreen(screen, screen.Update(msg))
	}
}

func TestHostLineAppearsOnlyAfterFirstSample(t *testing.T) {
	feed := &hostFeed{metrics: sampleMetrics()}
	screen, panel := buildScreen(screenOptions{width: 120, height: 24, host: feed})

	// "load[" rather than "load": the empty-table placeholder says "loading".
	if strings.Contains(screen.View(), "load[") {
		t.Errorf("meter band shown before any sample arrived:\n%s", screen.View())
	}

	sampleAll(screen, panel)

	view := screen.View()
	// Each meter prints its absolute amounts; the percentage is the bar, so
	// it is deliberately not repeated as text.
	for _, want := range []string{"load[", "0.50", "mem[", "1000M/2.0G", "disk[", "4.8G/19.1G", "up 1h30m"} {
		if !strings.Contains(view, want) {
			t.Errorf("meter band missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "50%") {
		t.Errorf("percentage repeated as text next to its own bar:\n%s", view)
	}
}

// A metrics blip must not clear the header: the last good sample stays,
// flagged, exactly as a failed service refresh keeps the last good table.
func TestFailedHostSampleKeepsLastAndMarksStale(t *testing.T) {
	feed := &hostFeed{metrics: sampleMetrics()}
	screen, panel := buildScreen(screenOptions{width: 120, height: 24, host: feed})
	sampleAll(screen, panel)

	feed.err = errors.New("connection lost")
	sampleAll(screen, panel)

	view := screen.View()
	if !strings.Contains(view, "0.50") {
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

	feed.err = nil
	sampleAll(screen, panel)
	if strings.Contains(screen.View(), "stale") {
		t.Errorf("stale flag survived a good sample:\n%s", screen.View())
	}
}

// bandLine is the second line of the screen, where the meter band sits.
func bandLine(view string) string {
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		return ""
	}
	return lines[1]
}

// Host metrics off means no band, however wide the terminal is: the whole
// point of that switch is that nothing extra runs on the server.
func TestNoBandWithoutHostMetrics(t *testing.T) {
	screen, panel := buildScreen(screenOptions{
		width: 200, height: 24, services: serviceList("web")})
	sampleAll(screen, panel)

	if strings.Contains(screen.View(), "load[") {
		t.Errorf("meter band shown with host metrics disabled:\n%s", screen.View())
	}
}

func TestBandFlagsStaleSample(t *testing.T) {
	feed := &hostFeed{metrics: sampleMetrics()}
	screen, panel := buildScreen(screenOptions{width: 120, height: 24, host: feed})
	sampleAll(screen, panel)
	feed.err = errors.New("connection lost")
	sampleAll(screen, panel)

	line := bandLine(screen.View())
	if !strings.Contains(line, "stale") {
		t.Errorf("stale sample not flagged in the band: %q", line)
	}
	if !strings.Contains(line, "load[") {
		t.Errorf("stale sample blanked the band: %q", line)
	}
}

// The summary is what says whether the project is in trouble without reading
// every row. It rides on the title line: the footer's hints already compete
// for every column they get.
func TestSummaryRidesOnTheTitleLine(t *testing.T) {
	screen, panel := buildScreen(screenOptions{width: 120, height: 24,
		host: &hostFeed{metrics: sampleMetrics()},
		services: []compose.Service{
			{Service: "web", Name: "app-web-1", State: "running", Status: "Up"},
			{Service: "api", Name: "app-api-1", State: "running", Status: "Up"},
			{Service: "cache", Name: "app-cache-1", State: "running", Health: "unhealthy", Status: "Up"},
			{Service: "migrate", Name: "app-migrate-1", State: "exited", Status: "Exited (0)"},
		}})
	sampleAll(screen, panel)

	title := strings.Split(screen.View(), "\n")[0]
	for _, want := range []string{"3 running", "1 exited", "1 unhealthy"} {
		if !strings.Contains(title, want) {
			t.Errorf("summary missing %q from the title line: %q", want, title)
		}
	}
	// The hints must not have been squeezed out to make room for it.
	if !strings.Contains(screen.View(), "q quit") {
		t.Errorf("summary cost the footer its hints:\n%s", screen.View())
	}
}

// A line wider than the screen wraps and shifts everything below it. This is
// the regression that the NET RX/TX and IO R/W columns first exposed at 110
// columns, where the last column's header-width floor pushed the header row
// past the terminal.
func TestViewNeverExceedsTheTerminalWidth(t *testing.T) {
	for _, width := range []int{70, 80, 100, 110, 130, 200} {
		screen, panel := buildScreen(screenOptions{width: width, height: 24,
			host:     &hostFeed{metrics: sampleMetrics()},
			services: serviceList("web", "db", "cache"),
			stats: []compose.ContainerStats{{Name: "app-web-1", CPUPerc: "12.34%",
				MemUsage: "153.6MiB / 2GiB", MemPerc: "7.5%",
				NetIO: "1.45GB / 892.3MB", BlockIO: "4.1kB / 0B"}},
		})
		sampleAll(screen, panel)

		for i, line := range strings.Split(screen.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("at %d columns, line %d is %d wide: %q", width, i, got, line)
			}
		}
	}
}

// The header block gets a blank line between it and the body, so the two do
// not read as one wall of text.
func TestHeaderBlockIsSeparatedFromTheBody(t *testing.T) {
	screen, panel := buildScreen(screenOptions{width: 100, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	sampleAll(screen, panel)

	lines := strings.Split(screen.View(), "\n")
	if strings.TrimSpace(lines[2]) != "" {
		t.Errorf("no blank line between the meter band and the body: %q", lines[2])
	}
}

// The screen is exactly as tall as the terminal: one row too many scrolls the
// header off the top, one too few leaves a gap above the footer.
func TestScreenIsExactlyAsTallAsTheTerminal(t *testing.T) {
	for _, height := range []int{10, 24, 40} {
		screen, panel := buildScreen(screenOptions{width: 120, height: height,
			host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web", "db")})
		sampleAll(screen, panel)

		if lines := strings.Count(screen.View(), "\n") + 1; lines != height {
			t.Errorf("at %d rows the screen drew %d lines", height, lines)
		}
	}
}

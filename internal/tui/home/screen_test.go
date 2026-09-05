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

// cgroupsFor builds a reading that yields the given docker-shaped stats when
// measured against a zero previous sample: the memory and I/O totals survive
// that, and the percentages are what a first reading cannot have.
func cgroupsFor(services []compose.Service, stats []compose.ContainerStats) compose.CgroupSample {
	sample := compose.CgroupSample{At: time.Now(),
		Containers: map[string]compose.CgroupReading{},
		Networks:   map[int]compose.CgroupReading{}}
	for index, service := range services {
		if index >= len(stats) {
			break
		}
		sample.Containers[service.ID] = compose.CgroupReading{
			MemBytes: 161061273, ReadBytes: 4100, WriteBytes: 8190}
	}
	return sample
}

type screenOptions struct {
	width, height int
	services      []compose.Service
	host          *hostFeed
	stats         []compose.ContainerStats
}

func buildScreen(options screenOptions) (*Model, *status.Model) {
	panel := status.New(status.Config{Stats: options.stats != nil})
	config := Config{
		Target:   "deploy@prod",
		Services: func() ([]compose.Service, error) { return options.services, nil },
	}
	if options.host != nil {
		config.Host = options.host.sample
	}
	if options.stats != nil {
		config.Stats = func([]compose.Service) (compose.CgroupSample, error) {
			return cgroupsFor(options.services, options.stats), nil
		}
	}
	screen := New(config, panel)
	screen.SetSize(options.width, options.height)
	return screen, panel
}

// sampleAll reads every source once and applies what comes back, which is what
// the first heartbeat does.
func sampleAll(screen *Model) {
	applyScreen(screen, screen.sampler.due())
}

// resample reads every source again on demand, the way `r` does. The sampler
// would otherwise refuse a second reading whose interval has not elapsed,
// which is the point of it.
func resample(screen *Model) {
	for _, id := range []sourceID{sourceServices, sourceHost, sourceStats} {
		applyScreen(screen, screen.sampler.read(id))
	}
}

// applyScreen runs a screen-owned command and feeds its message back, the way
// the Bubble Tea loop would. The two self-rearming ticks are not followed: a
// heartbeat or a drain tick would sleep for its interval and then ask to be
// scheduled again, forever.
func applyScreen(screen *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil, beatMsg, watchTickMsg:
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
	screen, _ := buildScreen(screenOptions{width: 120, height: 24, host: feed})

	// "load[" rather than "load": the empty-table placeholder says "loading".
	if strings.Contains(screen.View(), "load[") {
		t.Errorf("meter band shown before any sample arrived:\n%s", screen.View())
	}

	sampleAll(screen)

	view := screen.View()
	// Each meter prints its absolute amounts; the percentage is the bar, so
	// it is deliberately not repeated as text. The disk meter is labelled
	// with the mount point it is showing, since it follows the fullest
	// filesystem rather than always the root.
	for _, want := range []string{"load[", "0.50", "mem[", "1000M/2.0G", "/[", "4.8G/19.1G", "up 1h30m"} {
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
	screen, _ := buildScreen(screenOptions{width: 120, height: 24, host: feed})
	sampleAll(screen)

	feed.err = errors.New("connection lost")
	resample(screen)

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
	resample(screen)
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
	screen, _ := buildScreen(screenOptions{
		width: 200, height: 24, services: serviceList("web")})
	sampleAll(screen)

	if strings.Contains(screen.View(), "load[") {
		t.Errorf("meter band shown with host metrics disabled:\n%s", screen.View())
	}
}

func TestBandFlagsStaleSample(t *testing.T) {
	feed := &hostFeed{metrics: sampleMetrics()}
	screen, _ := buildScreen(screenOptions{width: 120, height: 24, host: feed})
	sampleAll(screen)
	feed.err = errors.New("connection lost")
	resample(screen)

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
	screen, _ := buildScreen(screenOptions{width: 120, height: 24,
		host: &hostFeed{metrics: sampleMetrics()},
		services: []compose.Service{
			{Service: "web", Name: "app-web-1", State: "running", Status: "Up"},
			{Service: "api", Name: "app-api-1", State: "running", Status: "Up"},
			{Service: "cache", Name: "app-cache-1", State: "running", Health: "unhealthy", Status: "Up"},
			{Service: "migrate", Name: "app-migrate-1", State: "exited", Status: "Exited (0)"},
		}})
	sampleAll(screen)

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
		screen, _ := buildScreen(screenOptions{width: width, height: 24,
			host:     &hostFeed{metrics: sampleMetrics()},
			services: serviceList("web", "db", "cache"),
			stats: []compose.ContainerStats{{Name: "app-web-1", CPUPerc: "12.34%",
				MemUsage: "153.6MiB / 2GiB", MemPerc: "7.5%",
				NetIO: "1.45GB / 892.3MB", BlockIO: "4.1kB / 0B"}},
		})
		sampleAll(screen)

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
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	sampleAll(screen)

	lines := strings.Split(screen.View(), "\n")
	if strings.TrimSpace(lines[2]) != "" {
		t.Errorf("no blank line between the meter band and the body: %q", lines[2])
	}
}

// The screen is exactly as tall as the terminal: one row too many scrolls the
// header off the top, one too few leaves a gap above the footer.
func TestScreenIsExactlyAsTallAsTheTerminal(t *testing.T) {
	for _, height := range []int{10, 24, 40} {
		screen, _ := buildScreen(screenOptions{width: 120, height: height,
			host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web", "db")})
		sampleAll(screen)

		if lines := strings.Count(screen.View(), "\n") + 1; lines != height {
			t.Errorf("at %d rows the screen drew %d lines", height, lines)
		}
	}
}

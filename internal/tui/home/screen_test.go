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
func TestSummaryRidesOnTheServicesRule(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 120, height: 24,
		host: &hostFeed{metrics: sampleMetrics()},
		services: []compose.Service{
			{Service: "web", Name: "app-web-1", State: "running", Status: "Up"},
			{Service: "api", Name: "app-api-1", State: "running", Status: "Up"},
			{Service: "cache", Name: "app-cache-1", State: "running", Health: "unhealthy", Status: "Up"},
			{Service: "migrate", Name: "app-migrate-1", State: "exited", Status: "Exited (0)"},
		}})
	sampleAll(screen)

	lines := strings.Split(screen.View(), "\n")
	rule := lines[headerHeight] // the services panel's own top rule
	for _, want := range []string{"3 running", "1 exited", "1 unhealthy"} {
		if !strings.Contains(rule, want) {
			t.Errorf("summary missing %q from the services rule: %q", want, rule)
		}
	}
	// It is about the project, so it must not have stayed on the header,
	// which is about the session and the machine.
	if header := strings.Join(lines[:headerHeight], " "); strings.Contains(header, "running") {
		t.Errorf("the project summary is still on the header: %q", header)
	}
	// The hints must not have been squeezed out to make room for it.
	if !strings.Contains(screen.View(), "q quit") {
		t.Errorf("summary cost the footer its hints:\n%s", screen.View())
	}
}

// A status is dropped whole or not at all: half of "1 unhealthy" is a number
// beside a word that no longer says which state it counts.
func TestTheSummaryIsDroppedRatherThanCut(t *testing.T) {
	for _, width := range []int{40, 50, 60, 70, 90, 120} {
		screen, _ := buildScreen(screenOptions{width: width, height: 24,
			host:     &hostFeed{metrics: sampleMetrics()},
			services: serviceList("web", "db", "cache")})
		sampleAll(screen)

		rule := strings.Split(screen.View(), "\n")[headerHeight]
		if strings.Contains(rule, "running") && !strings.Contains(rule, "3 running") {
			t.Errorf("at %d columns the summary was cut: %q", width, rule)
		}
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

// The header is a box, and a box is its own separator: the blank line that
// used to keep a bare band off the panel below it is now that panel's border.
// Both cost one row, which is why the arithmetic did not move.
func TestTheHeaderIsABoxWhenThereIsASampleForIt(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	sampleAll(screen)

	lines := strings.Split(screen.View(), "\n")
	if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[0], "linqode") {
		t.Errorf("the header does not open a titled box: %q", lines[0])
	}
	if !strings.Contains(lines[headerHeight-1], "└") {
		t.Errorf("the header box does not close on row %d: %q",
			headerHeight-1, lines[headerHeight-1])
	}
	// And the body starts immediately under it, with no blank row: two
	// borders touching is what every other pair of panels here already does.
	if !strings.Contains(lines[headerHeight], "┌") {
		t.Errorf("the body does not begin under the header: %q", lines[headerHeight])
	}
}

// Without a sample there is nothing to put in a box, and the session falls
// back to the bare line it always was — still two rows, so nothing below it
// moves.
func TestWithoutASampleTheHeaderIsAPlainLine(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		services: serviceList("web")})
	sampleAll(screen)

	lines := strings.Split(screen.View(), "\n")
	if strings.Contains(lines[0], "┌") {
		t.Errorf("a header with no band drew a box: %q", lines[0])
	}
	if !strings.Contains(lines[0], "linqode") {
		t.Errorf("the session is not on the first line: %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "" {
		t.Errorf("no blank line under the bare session line: %q", lines[1])
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

// A host the probe found without compose. Services is nil, which is how every
// capability this codebase does not have is expressed; the reason is what nil
// alone cannot say.
func noComposeScreen(width, height int, reason string, withHost bool) (*Model, *status.Model) {
	services := status.New(status.Config{Unavailable: reason})
	config := Config{
		Target: "deploy@prod", ComposeUnavailable: reason,
		OS: "Debian GNU/Linux 12 (bookworm)",
	}
	if withHost {
		feed := &hostFeed{metrics: sampleMetrics()}
		config.Host = feed.sample
		config.Processes = func() (host.ProcessSample, error) {
			return host.ProcessSample{UptimeSeconds: 1000, ClockTck: 100}, nil
		}
	}
	screen := New(config, services)
	screen.SetSize(width, height)
	sampleAll(screen)
	return screen, services
}

// The screen is built around what is left, not around what is missing. A
// table that can only explain its own absence must not hold the body at the
// largest size on the screen while the machine it is on has every reading it
// always had.
func TestNoComposeGivesTheBodyToTheMachine(t *testing.T) {
	screen, _ := noComposeScreen(150, 24, "docker is not installed on this host", true)

	if title := screen.panels[screen.anchor].Title(); title != "system" {
		t.Errorf("the anchor is %q, want the machine", title)
	}
	view := screen.View()
	if !strings.Contains(view, "docker is not installed on this host") {
		t.Error("the screen never says why there is no table")
	}
	// The readings that were always there are still there.
	for _, want := range []string{"memory", "uptime", "system"} {
		if !strings.Contains(view, want) {
			t.Errorf("the machine lost its %q row along with compose", want)
		}
	}
	// And nothing that cannot work is advertised.
	for _, gone := range []string{"c actions", "enter logs", "a live"} {
		if strings.Contains(view, gone) {
			t.Errorf("%q is offered on a host that cannot do it", gone)
		}
	}
	// What never needed a daemon stays.
	for _, kept := range []string{"x scripts", "! run"} {
		if !strings.Contains(view, kept) {
			t.Errorf("%q was withdrawn, and it never needed docker", kept)
		}
	}
}

// The on-demand tier is gated on somebody looking at the view that shows it.
// When that view is the body there is no `enter` to press, and a gate that
// waited for one would leave the process table permanently unread.
func TestTheOnDemandTierFollowsTheSystemViewToTheBody(t *testing.T) {
	screen, _ := noComposeScreen(150, 24, "docker is not installed on this host", true)
	if !screen.systemShown() {
		t.Fatal("the machine holds the body and is not considered shown")
	}
	if cmd := screen.sampler.read(sourceProcesses); cmd == nil {
		t.Error("the process table is never read on a screen that is showing it")
	}

	// And the gate still closes on a screen where the view is behind a band
	// nobody has opened.
	home, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	if home.systemShown() {
		t.Error("the machine is considered shown while the table holds the body")
	}
}

// With host_metrics off as well there is nothing behind the band either. The
// panel that at least says why is then the best thing to be looking at, and
// promoting an empty machine over it would be trading one blank panel for a
// worse one.
func TestNothingLeftToPromoteKeepsTheExplainingPanel(t *testing.T) {
	screen, _ := noComposeScreen(150, 20, "this host has docker-compose v1, which linqode does not drive", false)
	if title := screen.panels[screen.anchor].Title(); title != "services" {
		t.Errorf("the anchor is %q, want the panel that can explain itself", title)
	}
	if !strings.Contains(screen.View(), "docker-compose v1") {
		t.Error("the screen never says why there is no table")
	}
}

// The focus ring is the one thing on this screen with no other way in: the
// band and the feed cannot be reached without knowing that `tab` reaches
// them, and the band is where the machine's readings live. It is advertised
// exactly where it can be, and nowhere it would mislead.
func TestTheRingIsAdvertisedOnTheScreenThatHasOne(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)
	if !strings.Contains(screen.View(), "tab panels") {
		t.Error("nothing on the screen says the band and the feed can be reached")
	}

	// Inside a detail `tab` moves a focus nobody can see.
	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if screen.detail == nil {
		t.Fatal("the system view did not open")
	}
	if strings.Contains(screen.View(), "tab panels") {
		t.Error("the ring is advertised inside a detail, where it moves nothing visible")
	}

	// And a host with one panel has no ring to move around.
	alone, _ := noComposeScreen(150, 24, "docker is not installed on this host", true)
	if strings.Contains(alone.View(), "tab panels") {
		t.Error("the ring is advertised on a screen with a single panel")
	}
}

// `shift+tab` from the table reaches the band, which is the path an operator
// takes to the machine's readings. Pinned because the ring's order is what
// makes the hint above true.
func TestShiftTabFromTheTableReachesTheBand(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)
	if title := screen.focused().Title(); title != "services" {
		t.Fatalf("focus starts on %q, want the table", title)
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if title := screen.focused().Title(); title != "system" {
		t.Errorf("shift+tab from the table reaches %q, want the band", title)
	}
	// And `enter` there is what opens the readings behind it.
	applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if screen.detail == nil {
		t.Error("enter on the band did not open the system view")
	}
}

// A detail takes the whole body, and the focus ring with it. Moving around a
// ring nobody can see changes nothing on screen and everything about where
// `esc` lands: opening the system view from the header and coming back to the
// table is not a thing an operator asked for.
func TestTheRingDoesNotMoveBehindADetail(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)

	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if screen.detail == nil {
		t.Fatal("the system view did not open")
	}
	opened := screen.focus

	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if screen.focus != opened {
		t.Errorf("focus moved to %d behind the detail, from %d", screen.focus, opened)
	}

	// And coming back lands where it was left, on the header.
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if screen.detail != nil {
		t.Fatal("esc did not close the detail")
	}
	if title := screen.focused().Title(); title != "system" {
		t.Errorf("esc from the system view lands on %q, want the header", title)
	}
}

// `enter` descends one level, and a detail is the level below: there is
// nothing under it to open. Pressing it inside the system view used to run
// `open()` again, which re-read the whole on-demand tier — three SSH round
// trips for a keystroke that changed nothing on screen.
func TestEnterInsideADetailAsksTheHostForNothing(t *testing.T) {
	reads := 0
	feed := &hostFeed{metrics: sampleMetrics()}
	services := status.New(status.Config{})
	screen := New(Config{
		Target:   "deploy@prod",
		Host:     feed.sample,
		Services: func() ([]compose.Service, error) { return serviceList("api"), nil },
		Processes: func() (host.ProcessSample, error) {
			reads++
			return host.ProcessSample{UptimeSeconds: 1000, ClockTck: 100}, nil
		},
	}, services)
	screen.SetSize(150, 24)
	sampleAll(screen)

	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if screen.detail == nil {
		t.Fatal("the system view did not open")
	}
	opening := reads
	if opening == 0 {
		t.Fatal("opening the view did not read the process table")
	}

	for range 3 {
		applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	}
	if reads != opening {
		t.Errorf("three enters inside the view cost %d further reads", reads-opening)
	}
	if screen.detail == nil {
		t.Error("enter inside the system view closed it")
	}
}

// `esc` means "up one level" everywhere it appears — out of a detail, out of
// a menu, out of the `!` prompt. At the top there is no level above, so it
// does nothing. Leaving the application is `q`: the two keys are one
// keystroke apart and only one of them can be undone.
func TestEscNeverQuits(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)

	if cmd := screen.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("esc on the home quit the application")
		}
	}
	// And `q` from the same place still does.
	quitting, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	sampleAll(quitting)
	cmd := quitting.Update(key("q"))
	if cmd == nil {
		t.Fatal("q on the home did nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("q on the home did not quit")
	}
}

// Backing out of each level in turn, none of which may leave the application.
func TestEscBacksOutOfEveryLevel(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: []compose.Service{{Service: "api", Name: "p-api-1", State: "running"}},
		host:     &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)

	// The `!` prompt, where esc cancels what was typed.
	screen.Update(key("!"))
	if !screen.commandPrompt {
		t.Fatal("the command prompt did not open")
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if screen.commandPrompt {
		t.Error("esc did not cancel the command prompt")
	}

	// The action menu.
	screen.Update(key("c"))
	if screen.menu == nil {
		t.Fatal("the action menu did not open")
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if screen.menu != nil {
		t.Error("esc did not close the action menu")
	}

	// The system view.
	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if screen.detail == nil {
		t.Fatal("the system view did not open")
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if screen.detail != nil {
		t.Error("esc did not close the system view")
	}
	// And now at the top, where it stops rather than quitting.
	if cmd := screen.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("esc quit once there was nothing left to back out of")
		}
	}
}

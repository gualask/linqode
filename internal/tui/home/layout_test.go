package home

// Tests for the arithmetic that decides what fits, and for the focus ring the
// panels hang from. Both are checked away from rendering: a layout that is
// wrong by one row is a screen that scrolls its own header away.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/status"
)

func TestLayoutGivesTheHeaderItsBoxOnlyWhereThereAreMeters(t *testing.T) {
	boxed := layoutFor(100, 24, true, false)
	bare := layoutFor(100, 24, false, false)
	if !boxed.headerBox {
		t.Error("layout dropped the header box on a host that reports meters")
	}
	// A box is three rows against the bare line's two: the title line and
	// the blank rule under it pay for two of them, so it costs one.
	if got, want := bare.body.height-boxed.body.height, 1; got != want {
		t.Errorf("the header box cost %d rows, want %d", got, want)
	}
}

// The header and the footer are fixed; everything left over is the body's, so
// nothing is silently reserved for a panel that is not there.
func TestLayoutSpendsEveryRow(t *testing.T) {
	for _, height := range []int{8, 24, 60} {
		for _, band := range []bool{false, true} {
			for _, events := range []bool{false, true} {
				frame := layoutFor(100, height, band, events)
				header := titleLines + blankLines
				if band {
					header++
				}
				got := header + frame.body.height + frame.events.height + footerLine
				if got != height {
					t.Errorf("band=%v events=%v at %d rows: parts add up to %d",
						band, events, height, got)
				}
			}
		}
	}
}

// The satellite is what gives way. The table is the anchor and never shrinks
// below what makes it a table; the feed takes its rows back when the terminal
// cannot hold both.
func TestTheSatelliteYieldsBeforeTheAnchor(t *testing.T) {
	roomy := layoutFor(100, 30, true, true)
	if roomy.events.height != eventsBox {
		t.Errorf("the feed got %d rows at 30, want %d", roomy.events.height, eventsBox)
	}
	if roomy.body.height < anchorFloor {
		t.Errorf("the table was squeezed to %d rows", roomy.body.height)
	}

	// Somewhere on the way down the feed has to disappear rather than take
	// the table below its floor.
	for height := 30; height >= 6; height-- {
		frame := layoutFor(100, height, true, true)
		if frame.events.height > 0 && frame.body.height < anchorFloor {
			t.Fatalf("at %d rows the table was cut to %d to keep the feed",
				height, frame.body.height)
		}
	}
	if frame := layoutFor(100, 12, true, true); frame.events.height != 0 {
		t.Errorf("the feed kept %d rows on a terminal too short for both",
			frame.events.height)
	}
}

// A session with no stream has no feed panel, and its rows belong to the
// table rather than being reserved for something that will never arrive.
func TestNoRowsReservedForAFeedThatIsNotThere(t *testing.T) {
	frame := layoutFor(100, 30, true, false)
	if frame.events.height != 0 {
		t.Errorf("a feed panel was laid out with nothing to feed it: %+v", frame.events)
	}
	if frame.body.height != layoutFor(100, 30, true, true).body.height+eventsBox {
		t.Error("the feed's rows did not go back to the table")
	}
}

// A terminal shorter than the header and footer leaves no body at all, rather
// than a negative one that would panic on the way to being rendered.
func TestLayoutNeverReturnsANegativeBody(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4} {
		if got := layoutFor(80, height, true, false).body.height; got < 0 {
			t.Errorf("at %d rows the body is %d", height, got)
		}
	}
}

// Before the first WindowSizeMsg nothing is known, and the views render
// unbounded rather than to a guessed size.
func TestLayoutAtUnknownSize(t *testing.T) {
	frame := layoutFor(0, 0, true, false)
	if frame.body.width != 0 || frame.body.height != 0 {
		t.Errorf("unknown size produced a body of %+v", frame.body)
	}
}

// A border costs two cells on each axis, and the panel inside must never be
// told it has them.
func TestContentIsTheAreaInsideTheBorder(t *testing.T) {
	got := box{width: 40, height: 10}.content()
	if got.width != 38 || got.height != 8 {
		t.Errorf("content = %+v, want 38x8", got)
	}
	if tiny := (box{width: 1, height: 1}).content(); tiny.width != 0 || tiny.height != 0 {
		t.Errorf("content of a 1x1 box = %+v, want zeroes", tiny)
	}
}

// The body is a titled box, which is what says where the keys are pointing.
func TestBodyIsDrawnAsATitledPanel(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		services: serviceList("web")})
	sampleAll(screen)

	view := screen.View()
	if !strings.Contains(view, "─ services ─") {
		t.Errorf("the body lost its title:\n%s", view)
	}
	if !strings.Contains(view, "│") {
		t.Errorf("the body lost its border:\n%s", view)
	}
	if !strings.Contains(view, "SERVICE") {
		t.Errorf("the table is not inside the box:\n%s", view)
	}
}

// Focus starts at the top, on the machine — often the reason the session was
// opened — and `tab` walks the ring forward from there, in the order the
// panels sit on the screen, without falling off either end.
func TestTabWalksTheRingFromTheTop(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	sampleAll(screen)

	walk := []string{"system", "services", "system"}
	if got := screen.focused().Title(); got != walk[0] {
		t.Fatalf("focus started on %q, want %q", got, walk[0])
	}
	for _, want := range walk[1:] {
		screen.Update(tea.KeyMsg{Type: tea.KeyTab})
		if got := screen.focused().Title(); got != want {
			t.Fatalf("tab reached %q, want %q", got, want)
		}
	}
	// And the other way round, off the top and onto the last stop.
	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := screen.focused().Title(); got != "services" {
		t.Errorf("shift+tab from the top reached %q, want the table", got)
	}
}

// Enter on the band opens the system view over the body; esc comes back. The
// band stays on the header throughout — it is the header, not a panel that
// takes its turn in the body.
func TestEnterOnTheBandOpensTheSystemView(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 120, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	sampleAll(screen)

	openSystemView(screen)
	if screen.detail == nil {
		t.Fatal("enter on the band opened nothing")
	}
	view := screen.View()
	if !strings.Contains(view, "─ system ─") {
		t.Errorf("the system view is not on screen:\n%s", view)
	}
	if !strings.Contains(view, "0.50") || !strings.Contains(view, "available") {
		t.Errorf("the system view is missing its readings:\n%s", view)
	}
	if !strings.Contains(bandLine(view), "load[") {
		t.Errorf("the band left the header while its view was open:\n%s", view)
	}
	if !strings.Contains(view, "esc back") {
		t.Errorf("the way out is not on the footer:\n%s", view)
	}

	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if screen.detail != nil {
		t.Error("esc did not close the system view")
	}
	if !strings.Contains(screen.View(), "─ services ─") {
		t.Error("the table did not come back")
	}
}

// Nothing to show, nothing to open: without a sample the band is not on the
// header, and enter on it must not put an empty box over the table.
func TestTheSystemViewNeedsASample(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		services: serviceList("web")})
	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	screen.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if screen.detail != nil {
		t.Error("the system view opened without a sample behind it")
	}
}

// Refresh is the screen's command: it reads everything again, whichever
// region happens to hold focus.
func TestRefreshResamplesEverythingFromEitherStop(t *testing.T) {
	services, hosts := 0, 0
	screen := New(Config{
		Services: func() ([]compose.Service, error) { services++; return nil, nil },
		Host:     func() (host.Metrics, error) { hosts++; return host.Metrics{}, nil },
	}, status.New(status.Config{}))

	applyScreen(screen, screen.Update(key("r")))
	if services != 1 || hosts != 1 {
		t.Fatalf("with the table focused: %d service reads, %d host reads", services, hosts)
	}

	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	applyScreen(screen, screen.Update(key("r")))
	if services != 2 || hosts != 2 {
		t.Errorf("with the band focused: %d service reads, %d host reads", services, hosts)
	}
}

// Without a fetch nothing is read and the band never claims its row.
func TestNoHostFetchNoBand(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24})
	if cmd := screen.sampler.read(sourceHost); cmd != nil {
		t.Error("a sample was taken with no fetch configured")
	}
	if screen.frame().headerBox {
		t.Error("the header boxed meters this host will never report")
	}
}

// The on-demand tier: a reading that is never taken until the view that
// shows it is open, and is taken the moment it opens rather than at the next
// beat. It is the point of the panel model — a panel nobody is looking at
// costs nothing on the wire.
func TestTheProcessTableIsReadOnlyWhileItsViewIsOpen(t *testing.T) {
	reads := 0
	screen := New(Config{
		Target:   "deploy@prod",
		Services: func() ([]compose.Service, error) { return serviceList("web"), nil },
		Host:     func() (host.Metrics, error) { return host.Metrics{CPUs: 4}, nil },
		Processes: func() (host.ProcessSample, error) {
			reads++
			return host.ProcessSample{UptimeSeconds: float64(reads)}, nil
		},
	}, status.New(status.Config{}))
	screen.SetSize(120, 30)

	sampleAll(screen)
	sampleAll(screen)
	if reads != 0 {
		t.Fatalf("the process table was read %d times with the view closed", reads)
	}

	// Opening the view asks for it: waiting for the next beat would be
	// waiting for a source that was not due a moment ago.
	openSystemView(screen)
	if reads != 1 {
		t.Fatalf("opening the view read the process table %d times", reads)
	}

	// And closing it stops the source again.
	applyScreen(screen, screen.Update(tea.KeyMsg{Type: tea.KeyEsc}))
	screen.sampler.sources[sourceProcesses].started = time.Time{}
	sampleAll(screen)
	if reads != 1 {
		t.Errorf("the process table was still read after the view closed: %d", reads)
	}
}

// The footer inside a detail says the way out *and* what the detail itself
// answers to. Its header form offers `enter`, which is the key just pressed.
func TestTheDetailsOwnKeysReachTheFooter(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 140, height: 30,
		services: serviceList("web"), host: &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)
	openSystemView(screen)

	footer := screen.footer()
	if !strings.Contains(footer, "esc back") {
		t.Errorf("the way out is missing from the detail's footer: %q", footer)
	}
	if !strings.Contains(footer, "s by cpu") {
		t.Errorf("the detail's own key is missing from its footer: %q", footer)
	}
	if strings.Contains(footer, "enter system") {
		t.Errorf("the footer still offers the way in: %q", footer)
	}
	// Back on the home the band offers the way in again.
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if footer := screen.footer(); !strings.Contains(footer, "enter system") {
		t.Errorf("the band lost its hint after a detail closed: %q", footer)
	}
}

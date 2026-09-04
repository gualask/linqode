package home

// Tests for the arithmetic that decides what fits, and for the focus ring the
// panels hang from. Both are checked away from rendering: a layout that is
// wrong by one row is a screen that scrolls its own header away.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/status"
)

func TestLayoutGivesTheBandItsRowOnlyWhenThereIsASample(t *testing.T) {
	withBand := layoutFor(100, 24, true)
	without := layoutFor(100, 24, false)
	if !withBand.band {
		t.Error("layout dropped the band although there is a sample")
	}
	if got, want := without.body.height-withBand.body.height, 1; got != want {
		t.Errorf("the band cost %d rows, want %d", got, want)
	}
}

// The header and the footer are fixed; everything left over is the body's, so
// nothing is silently reserved for a panel that is not there.
func TestLayoutSpendsEveryRow(t *testing.T) {
	for _, height := range []int{8, 24, 60} {
		for _, band := range []bool{false, true} {
			frame := layoutFor(100, height, band)
			header := titleLines + blankLines
			if band {
				header++
			}
			if got := header + frame.body.height + footerLine; got != height {
				t.Errorf("band=%v at %d rows: parts add up to %d", band, height, got)
			}
		}
	}
}

// A terminal shorter than the header and footer leaves no body at all, rather
// than a negative one that would panic on the way to being rendered.
func TestLayoutNeverReturnsANegativeBody(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4} {
		if got := layoutFor(80, height, true).body.height; got < 0 {
			t.Errorf("at %d rows the body is %d", height, got)
		}
	}
}

// Before the first WindowSizeMsg nothing is known, and the views render
// unbounded rather than to a guessed size.
func TestLayoutAtUnknownSize(t *testing.T) {
	frame := layoutFor(0, 0, true)
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

// Focus starts on the table — the band is what an operator reads, the table
// is what they act on — and tab walks the ring in both directions without
// falling off either end.
func TestTabWalksTheRing(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	if screen.focus != screen.anchor {
		t.Fatalf("focus started at %d, want the table at %d", screen.focus, screen.anchor)
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	if screen.focus == screen.anchor {
		t.Error("tab did not leave the table")
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	if screen.focus != screen.anchor {
		t.Error("tab did not come back round to the table")
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if screen.focus == screen.anchor {
		t.Error("shift-tab did not walk the ring the other way")
	}
}

// Enter on the band opens the system view over the body; esc comes back. The
// band stays on the header throughout — it is the header, not a panel that
// takes its turn in the body.
func TestEnterOnTheBandOpensTheSystemView(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 120, height: 24,
		host: &hostFeed{metrics: sampleMetrics()}, services: serviceList("web")})
	sampleAll(screen)

	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	screen.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

// Esc on the home quits, as it always has; only a detail intercepts it.
func TestEscQuitsFromTheHome(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		services: serviceList("web")})
	if cmd := screen.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Error("esc on the home did not quit")
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
	if layoutFor(100, 24, screen.system.HasBand()).band {
		t.Error("the header reserved a row for a band that cannot exist")
	}
}

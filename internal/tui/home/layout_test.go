package home

// Tests for the arithmetic that decides what fits, and for the focus ring the
// panels hang from. Both are checked away from rendering: a layout that is
// wrong by one row is a screen that scrolls its own header away.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	screen, panel := buildScreen(screenOptions{width: 100, height: 24,
		services: serviceList("web")})
	apply(panel, panel.Sample())

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

// With one panel the ring is a fixed point: tab must not walk off it, and the
// only panel there is keeps focus.
func TestFocusRingHoldsWithASinglePanel(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 100, height: 24,
		services: serviceList("web")})
	for range 3 {
		screen.Update(tea.KeyMsg{Type: tea.KeyTab})
		screen.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	}
	if screen.focus != 0 {
		t.Errorf("focus walked to %d with one panel", screen.focus)
	}
	if screen.focused() != screen.panels[0] {
		t.Error("the ring lost track of its only panel")
	}
}

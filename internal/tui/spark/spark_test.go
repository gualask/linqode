package spark

// What is pinned here is mostly the scaling: a strip drawn against the wrong
// window is not wrong by a little, it says the opposite of what happened.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func plain(int) lipgloss.Style { return lipgloss.NewStyle() }

// A reading that barely moves must not be drawn as if it had swung from
// empty to full, and one that spans more than the floor keeps its own range.
func TestPercentWidensANarrowWindowAndNoMore(t *testing.T) {
	low, high := Percent([]float64{50, 50.5, 50.2})
	if high-low < MinimumSpan {
		t.Errorf("narrow window drawn against %v–%v", low, high)
	}
	low, high = Percent([]float64{10, 60, 35})
	if low != 10 || high != 60 {
		t.Errorf("wide window widened to %v–%v", low, high)
	}
	// A percentage cannot go outside its own bounds, however the widening
	// falls.
	low, high = Percent([]float64{99, 100})
	if low < 0 || high > 100 {
		t.Errorf("scale left the percentage range: %v–%v", low, high)
	}
}

// A container's CPU is not a share of anything finite, so its window is not
// clamped at a hundred: two busy cores are not drawn as one.
func TestWindowIsNotClampedAboveAHundred(t *testing.T) {
	low, high := Window([]float64{150, 250}, MinimumSpan)
	if low != 150 || high != 250 {
		t.Errorf("window = %v–%v, want 150–250", low, high)
	}
	strip := []rune(Strip([]float64{150, 250}, low, high, plain))
	if strip[1] != '█' || strip[0] == '█' {
		t.Errorf("a climb past a hundred drew %q", string(strip))
	}
}

// An amount with no natural full is widened by a share of itself: four
// hundred megabytes moving by one is flat, moving by sixty is a climb.
func TestRelativeWidensByAShareOfTheLargest(t *testing.T) {
	low, high := Relative([]float64{400, 401}, 0.1)
	if high-low < 40 {
		t.Errorf("a one-in-four-hundred wobble drawn against %v–%v", low, high)
	}
	low, high = Relative([]float64{400, 460}, 0.1)
	if low != 400 || high != 460 {
		t.Errorf("a fifteen percent climb widened to %v–%v", low, high)
	}
}

// A rate has no natural full, so it is anchored at nothing and scaled to the
// busiest moment: the strip says when the traffic happened.
func TestRateAnchorsAtZero(t *testing.T) {
	low, high := Rate([]float64{100, 4000, 250})
	if low != 0 || high != 4000 {
		t.Errorf("rate scale = %v–%v, want 0–4000", low, high)
	}
}

// Memory sitting between 78% and 88% is exactly the case a fixed 0–100 scale
// flattens into eight solid blocks: the meter beside it already says the
// level, so the strip has to say the shape.
func TestAClimbIsVisibleInTheStrip(t *testing.T) {
	values := []float64{78, 80, 82, 84, 86, 88}
	floor, ceiling := Percent(values)
	strip := []rune(Strip(values, floor, ceiling, plain))
	if strip[0] == strip[len(strip)-1] {
		t.Errorf("a ten-point climb drew flat: %q", string(strip))
	}
}

// The colour is the caller's, asked about each cell by its index.
func TestStripAsksTheStyleAboutEveryCell(t *testing.T) {
	var asked []int
	Strip([]float64{1, 2, 3}, 0, 3, func(index int) lipgloss.Style {
		asked = append(asked, index)
		return lipgloss.NewStyle()
	})
	if len(asked) != 3 || asked[0] != 0 || asked[2] != 2 {
		t.Errorf("style asked about cells %v", asked)
	}
}

// A bar keeps its width whatever it draws, so the text after it lines up, and
// a share above zero is never drawn as nothing.
func TestBar(t *testing.T) {
	for _, fraction := range []float64{0, 0.001, 0.5, 1, 3} {
		if got := len([]rune(Bar(fraction, 8))); got != 8 {
			t.Errorf("Bar(%v, 8) is %d cells wide", fraction, got)
		}
	}
	if got := Bar(0, 4); strings.TrimSpace(got) != "" {
		t.Errorf("nothing drew %q", got)
	}
	if got := Bar(0.001, 4); !strings.HasPrefix(got, "▏") {
		t.Errorf("a sliver drew %q, want the thinnest mark", got)
	}
	if got := Bar(1, 4); got != "████" {
		t.Errorf("a whole drew %q", got)
	}
	if got := Bar(0.5, 4); got != "██  " {
		t.Errorf("a half drew %q", got)
	}
	if got := Bar(0.5, 0); got != "" {
		t.Errorf("zero width drew %q", got)
	}
}

// A histogram stacks its heights across rows, draws any value above zero,
// and leaves an empty column blank rather than on a floor.
func TestColumns(t *testing.T) {
	lines := Columns([]float64{0, 1, 50, 100}, 100, 2, plain)
	if len(lines) != 2 {
		t.Fatalf("drew %d rows, want 2", len(lines))
	}
	top, bottom := []rune(lines[0]), []rune(lines[1])
	if top[0] != ' ' || bottom[0] != ' ' {
		t.Errorf("an empty column is not blank: %q / %q", lines[0], lines[1])
	}
	if top[1] != ' ' || bottom[1] != '▁' {
		t.Errorf("one in a hundred drew %q over %q, want the lowest mark", top[1], bottom[1])
	}
	if top[2] != ' ' || bottom[2] != '█' {
		t.Errorf("a half drew %q over %q, want one full row", top[2], bottom[2])
	}
	if top[3] != '█' || bottom[3] != '█' {
		t.Errorf("the ceiling drew %q over %q, want both rows full", top[3], bottom[3])
	}
	if got := Columns([]float64{1}, 1, 0, plain); got != nil {
		t.Errorf("no rows drew %q", got)
	}
}

// The span is measured from the samples that arrived, not from the cadence
// that was asked for.
func TestSpan(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{45, "45s"},
		{90, "2m"},
		{600, "10m"},
		{7200, "2h"},
	}
	for _, c := range cases {
		if got := Span(c.seconds); got != c.want {
			t.Errorf("Span(%v) = %q, want %q", c.seconds, got, c.want)
		}
	}
}

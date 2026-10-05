package spark

// What is pinned here is mostly the scaling: a strip drawn against the wrong
// window is not wrong by a little, it says the opposite of what happened.

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func plain(int) lipgloss.Style { return lipgloss.NewStyle() }

// bare is a strip or a chart standing on the terminal's own background,
// which is every one of these tests: what a track is for is a question for
// the callers that have gauges to line up with.
var bare = lipgloss.NewStyle()

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
	strip := []rune(Strip([]float64{150, 250}, low, high, plain, bare))
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
	strip := []rune(Strip(values, floor, ceiling, plain, bare))
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
	}, bare)
	if len(asked) != 3 || asked[0] != 0 || asked[2] != 2 {
		t.Errorf("style asked about cells %v", asked)
	}
}

// A bar keeps its width whatever it draws, so the text after it lines up, and
// a share above zero is never drawn as nothing.
func TestBar(t *testing.T) {
	for _, fraction := range []float64{0, 0.001, 0.5, 1, 3} {
		if got := len([]rune(Bar(fraction, 8, bare, bare))); got != 8 {
			t.Errorf("Bar(%v, 8) is %d cells wide", fraction, got)
		}
	}
	if got := Bar(0, 4, bare, bare); strings.TrimSpace(got) != "" {
		t.Errorf("nothing drew %q", got)
	}
	if got := Bar(0.001, 4, bare, bare); !strings.HasPrefix(got, "▏") {
		t.Errorf("a sliver drew %q, want the thinnest mark", got)
	}
	if got := Bar(1, 4, bare, bare); got != "████" {
		t.Errorf("a whole drew %q", got)
	}
	if got := Bar(0.5, 4, bare, bare); got != "██  " {
		t.Errorf("a half drew %q", got)
	}
	if got := Bar(0.5, 0, bare, bare); got != "" {
		t.Errorf("zero width drew %q", got)
	}
}

// A histogram stacks its heights across rows, draws any value above zero,
// and leaves an empty column blank rather than on a floor.
func TestColumns(t *testing.T) {
	lines := Columns([]float64{0, 1, 50, 100}, 100, 2, plain, bare)
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
	if got := Columns([]float64{1}, 1, 0, plain, bare); got != nil {
		t.Errorf("no rows drew %q", got)
	}
}

// A column is solid to its top cell, which is a half where the value falls
// between two rows. The columns touch, an empty one is the bare track rather
// than a floor, and anything above zero is drawn.
func TestBars(t *testing.T) {
	lines := Bars([]float64{0, 12.5, 50, 100}, 100, 2, plain, bare)
	want := []string{"   █", " ▄██"}
	if len(lines) != len(want) {
		t.Fatalf("two rows drew %d lines: %q", len(lines), lines)
	}
	for row := range want {
		if lines[row] != want[row] {
			t.Errorf("row %d is %q, want %q", row, lines[row], want[row])
		}
	}
	// A reading far below one column's own height is still a reading.
	if got := Bars([]float64{0.4}, 100, 4, plain, bare); got[3] != "▄" {
		t.Errorf("0.4%% of a hundred drew %q in the bottom row", got[3])
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

// A meter keeps its width, clamps a reading past either end, and fills in
// proportion.
func TestMeter(t *testing.T) {
	for _, percent := range []float64{-20, 0, 37, 100, 250} {
		if got := lipgloss.Width(Meter(percent, 10, bare, bare)); got != 10 {
			t.Errorf("Meter(%v, 10) is %d cells wide", percent, got)
		}
	}
	if got := strings.Count(Meter(50, 10, bare, bare), "█"); got != 5 {
		t.Errorf("half a meter filled %d cells of 10", got)
	}
	if got := strings.Count(Meter(250, 10, bare, bare), "█"); got != 10 {
		t.Errorf("a reading past a hundred filled %d cells of 10", got)
	}
	if got := Meter(50, 0, bare, bare); got != "" {
		t.Errorf("zero width drew %q", got)
	}
}

// Shares drawn as one bar: the whole width, in proportion, with a share
// above zero never rounded away and a level with nothing in it absent.
func TestSegments(t *testing.T) {
	width := func(values []float64, cells int) string {
		return Segments(values, cells, plain, bare)
	}
	if got := width([]float64{1, 1}, 10); got != strings.Repeat("█", 10) {
		t.Errorf("two equal shares drew %q", got)
	}
	if got := len([]rune(width([]float64{97, 2, 1}, 20))); got != 20 {
		t.Errorf("three shares drew %d cells, want 20", got)
	}
	// One error among a thousand lines is one cell, not none, and the
	// hundredth that pays for it comes off the widest share.
	segments := Segments([]float64{999, 1}, 20, func(index int) lipgloss.Style {
		if index == 1 {
			return lipgloss.NewStyle().Bold(true)
		}
		return lipgloss.NewStyle()
	}, bare)
	if got := len([]rune(segments)); got != 20 {
		t.Errorf("a sliver beside a whole drew %d cells: %q", got, segments)
	}
	// Nothing at all is the bare track, and no width is nothing.
	if got := width([]float64{0, 0}, 4); got != "    " {
		t.Errorf("no shares drew %q, want the track", got)
	}
	if got := width([]float64{1}, 0); got != "" {
		t.Errorf("no width drew %q", got)
	}
}

// A reading that is not a number, or too large to be a count of cells, must
// not reach an integer conversion unclamped: NaN became a negative repeat
// count and panicked, and +Inf drew as nothing at all. Not a number is drawn
// as the smallest shape and an unbounded reading as the largest.
func TestReadingsThatAreNotNumbersDrawAtTheirBounds(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	if got, want := Bar(nan, 8, bare, bare), Bar(0, 8, bare, bare); got != want {
		t.Errorf("Bar(NaN) = %q, want %q", got, want)
	}
	if got, want := Meter(nan, 8, bare, bare), Meter(0, 8, bare, bare); got != want {
		t.Errorf("Meter(NaN) = %q, want %q", got, want)
	}
	if got, want := Bar(inf, 8, bare, bare), Bar(1, 8, bare, bare); got != want {
		t.Errorf("Bar(+Inf) = %q, want %q", got, want)
	}
	if got := Cell(inf); got != "█" {
		t.Errorf("Cell(+Inf) = %q, want a full cell", got)
	}
	if got := Cell(nan); got != "▁" {
		t.Errorf("Cell(NaN) = %q, want the lowest cell", got)
	}
	shapes := map[string]func([]float64, float64, int,
		func(int) lipgloss.Style, lipgloss.Style) []string{"Bars": Bars, "Columns": Columns}
	for name, draw := range shapes {
		lines := draw([]float64{inf, nan, 1e300}, 1, 2, plain, bare)
		for _, column := range []int{0, 2} {
			if top := []rune(lines[0])[column]; top != '█' {
				t.Errorf("%s: column %d tops out at %q, want full:\n%s",
					name, column, top, strings.Join(lines, "\n"))
			}
		}
		if bottom := []rune(lines[1])[1]; bottom != ' ' {
			t.Errorf("%s: NaN drew %q, want nothing", name, bottom)
		}
	}
	if got := Segments([]float64{nan, 1}, 4, plain, bare); lipgloss.Width(got) != 4 {
		t.Errorf("Segments with NaN = %q, want four cells", got)
	}
}

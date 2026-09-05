package system

// Tests for the remembered samples and the strips they draw. What is being
// pinned here is mostly the scaling: a strip drawn against the wrong window
// is not wrong by a little, it says the opposite of what happened.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/theme"
)

// climbing is a model fed rounds samples five host-seconds apart, in which
// memory rises steadily and one core does all the work.
func climbing(rounds int) *Model {
	m := New("", "")
	for round := 1; round <= rounds; round++ {
		metrics := richMetrics()
		metrics.UptimeSeconds = 5400 + float64(round*5)
		metrics.MemAvailableKB = metrics.MemTotalKB - uint64(round)*20_000
		metrics.CPUTimes = nil
		machine := host.CPUTime{Name: "cpu"}
		for core := range 4 {
			// Cumulative counters: cpu0 busy, the rest idle.
			busy := uint64(0)
			if core == 0 {
				busy = uint64(round) * 250
			}
			counter := host.CPUTime{Name: "cpu" + string(rune('0'+core)),
				Total: uint64(round) * 500, Idle: uint64(round)*500 - busy}
			metrics.CPUTimes = append(metrics.CPUTimes, counter)
			machine.Total += counter.Total
			machine.Idle += counter.Idle
		}
		metrics.CPUTimes = append([]host.CPUTime{machine}, metrics.CPUTimes...)
		// Traffic that comes and goes, so the strip has a shape rather
		// than the flat top a perfectly steady rate would draw.
		var carried uint64
		for r := 1; r <= round; r++ {
			carried += uint64(10_000 * (1 + r%4))
		}
		metrics.Interfaces = []host.Interface{
			{Name: "eth0", RxBytes: carried, TxBytes: carried / 8},
		}
		m.SetSample(metrics, nil)
	}
	return m
}

// The ring keeps the newest samples and hands them back oldest first.
func TestHistoryKeepsTheNewest(t *testing.T) {
	var h history
	for round := range historyDepth + 40 {
		h.push(trend{uptime: float64(round), cpu: float64(round)})
	}
	samples, seconds := h.window(sparkWidth)
	if len(samples) != sparkWidth {
		t.Fatalf("window holds %d samples, want %d", len(samples), sparkWidth)
	}
	if samples[0].cpu >= samples[len(samples)-1].cpu {
		t.Errorf("window is not oldest first: %v … %v", samples[0], samples[len(samples)-1])
	}
	if want := float64(sparkWidth - 1); seconds != want {
		t.Errorf("window covers %v seconds, want %v", seconds, want)
	}
	if len(h.samples) > historyDepth {
		t.Errorf("ring grew to %d", len(h.samples))
	}
}

// One sample is not a trend, and neither is none.
func TestHistoryNeedsTwoSamples(t *testing.T) {
	var h history
	h.push(trend{uptime: 1})
	if samples, _ := h.window(sparkWidth); samples != nil {
		t.Errorf("one sample yielded a window: %v", samples)
	}
}

// A reading that barely moves must not be drawn as if it had swung from
// empty to full, and one that spans more than the floor keeps its own range.
func TestPercentScaleWidensANarrowWindowAndNoMore(t *testing.T) {
	low, high := percentScale([]float64{50, 50.5, 50.2})
	if high-low < minimumSpan {
		t.Errorf("narrow window drawn against %v–%v", low, high)
	}
	low, high = percentScale([]float64{10, 60, 35})
	if low != 10 || high != 60 {
		t.Errorf("wide window widened to %v–%v", low, high)
	}
	// A percentage cannot go outside its own bounds, however the widening
	// falls.
	low, high = percentScale([]float64{99, 100})
	if low < 0 || high > 100 {
		t.Errorf("scale left the percentage range: %v–%v", low, high)
	}
}

// A rate has no natural full, so it is anchored at nothing and scaled to the
// busiest moment: the strip says when the traffic happened.
func TestRateScaleAnchorsAtZero(t *testing.T) {
	low, high := rateScale([]float64{100, 4000, 250})
	if low != 0 || high != 4000 {
		t.Errorf("rate scale = %v–%v, want 0–4000", low, high)
	}
}

// Memory sitting between 78% and 88% is exactly the case a fixed 0–100 scale
// flattens into eight solid blocks: the meter beside it already says the
// level, so the strip has to say the shape.
func TestAClimbIsVisibleInTheStrip(t *testing.T) {
	values := []float64{78, 80, 82, 84, 86, 88}
	floor, ceiling := percentScale(values)
	strip := sparkline(values, floor, ceiling, func(float64) lipgloss.Style { return theme.Green })
	if first, last := []rune(strip)[0], []rune(strip)[len([]rune(strip))-1]; first == last {
		t.Errorf("a ten-point climb drew flat: %q", strip)
	}
	// And on a fixed scale it would not have.
	flat := sparkline(values, 0, 100, func(float64) lipgloss.Style { return theme.Green })
	if first, last := []rune(flat)[0], []rune(flat)[len([]rune(flat))-1]; first != last {
		t.Logf("the fixed scale distinguishes these after all: %q", flat)
	}
}

// The strips are a column: either all of them fit at the right edge or none
// of them are drawn, because one appearing on a single row reads as data
// about that row rather than as the width running out.
func TestStripsAreAColumnOrNothing(t *testing.T) {
	m := climbing(10)

	m.SetSize(160, 24)
	wide := m.View()
	var columns []int
	for _, line := range strings.Split(wide, "\n") {
		if column := stripStart(line); column >= 0 {
			columns = append(columns, column)
		}
	}
	if len(columns) != 3 {
		t.Fatalf("expected a strip on cpu, memory and net, got %d:\n%s", len(columns), wide)
	}
	// They also have to start at the same column to read as a column.
	for _, column := range columns {
		if column != columns[0] {
			t.Errorf("strips start at different columns: %v", columns)
			break
		}
	}

	m.SetSize(90, 24)
	for _, line := range strings.Split(m.View(), "\n") {
		if stripStart(line) >= 0 {
			t.Errorf("a strip survived a terminal with no room for the column:\n%q", line)
		}
	}
}

// sparkGlyphs is every height a strip can be drawn at.
const sparkGlyphs = "▁▂▃▄▅▆▇█"

// stripStart is the column a trend strip begins at, or -1 for a line that
// has none. A strip is the last thing on its line, which is what separates
// it from the meter bars and the per-core row — both drawn from the same
// glyphs, neither of them at the end.
func stripStart(line string) int {
	runes := []rune(line)
	if len(runes) == 0 || !strings.ContainsRune(sparkGlyphs, runes[len(runes)-1]) {
		return -1
	}
	start := len(runes)
	for start > 0 && strings.ContainsRune(sparkGlyphs, runes[start-1]) {
		start--
	}
	return lipgloss.Width(string(runes[:start]))
}

// The span is measured from the samples that arrived, not from the cadence
// that was asked for.
func TestSparkSpan(t *testing.T) {
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
		if got := sparkSpan(c.seconds); got != c.want {
			t.Errorf("sparkSpan(%v) = %q, want %q", c.seconds, got, c.want)
		}
	}
}

package system

// Tests for the remembered samples and the strips they draw. What is being
// pinned here is mostly the scaling: a strip drawn against the wrong window
// is not wrong by a little, it says the opposite of what happened.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/spark"
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

// stripStart is the column a trend strip begins at, or -1 for a line that
// has none. A strip is the last thing on its line, which is what separates
// it from the meter bars and the per-core row — both drawn from the same
// glyphs, neither of them at the end.
func stripStart(line string) int {
	runes := []rune(line)
	if len(runes) == 0 || !strings.ContainsRune(spark.Glyphs, runes[len(runes)-1]) {
		return -1
	}
	start := len(runes)
	for start > 0 && strings.ContainsRune(spark.Glyphs, runes[start-1]) {
		start--
	}
	return lipgloss.Width(string(runes[:start]))
}

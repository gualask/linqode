package system

// Tests for the remembered samples and the strips they draw. What is being
// pinned here is mostly the scaling: a strip drawn against the wrong window
// is not wrong by a little, it says the opposite of what happened.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/spark"
)

// climbing is a model fed rounds samples five host-seconds apart, in which
// memory rises steadily and one core does all the work.
func climbing(rounds int) *Model {
	return adjusted(rounds, func(int, *host.Metrics) {})
}

// adjusted is climbing with a say in each round's sample before it is applied.
func adjusted(rounds int, adjust func(round int, metrics *host.Metrics)) *Model {
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
		adjust(round, &metrics)
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
	samples, seconds := h.window(stripMinimum)
	if len(samples) != stripMinimum {
		t.Fatalf("window holds %d samples, want %d", len(samples), stripMinimum)
	}
	if samples[0].cpu >= samples[len(samples)-1].cpu {
		t.Errorf("window is not oldest first: %v … %v", samples[0], samples[len(samples)-1])
	}
	if want := float64(stripMinimum - 1); seconds != want {
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
	if samples, _ := h.window(stripMinimum); samples != nil {
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

// A wide terminal reaches further back than a narrow one, and no further than
// the history goes.
func TestStripsWidenWithTheRoom(t *testing.T) {
	m := climbing(historyDepth + 20)
	lengths := map[int]int{}
	for _, width := range []int{170, 260, 400} {
		m.SetSize(width, 40)
		for _, line := range strings.Split(m.View(), "\n") {
			if column := stripStart(line); column >= 0 {
				lengths[width] = lipgloss.Width(line) - column
				break
			}
		}
	}
	if lengths[170] < stripMinimum || lengths[260] <= lengths[170] {
		t.Errorf("strip lengths by terminal width: %v", lengths)
	}
	if lengths[400] > historyDepth {
		t.Errorf("a strip of %d cells from a history of %d", lengths[400], historyDepth)
	}
}

// filling is a model whose /var grows by perRound kilobytes every five host
// seconds, out of ten million.
func filling(rounds int, perRound uint64) *Model {
	return adjusted(rounds, func(round int, metrics *host.Metrics) {
		metrics.Filesystems = []host.Filesystem{{Device: "/dev/sdb1", Mount: "/var",
			TotalKB: 10_000_000, UsedKB: 9_000_000 + uint64(round)*perRound}}
	})
}

func fillText(m *Model) string {
	m.SetSize(160, 40)
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "/dev/sdb1") {
			return line
		}
	}
	return ""
}

// A thousand kilobytes a second into the last million says a quarter of an
// hour, before the device name.
func TestAFillingDiskSaysWhenItWillBeFull(t *testing.T) {
	line := fillText(filling(20, 5_000))
	if !strings.Contains(line, "full in ~15m") {
		t.Errorf("no fill time on a disk filling in fifteen minutes:\n%s", line)
	}
	if strings.Index(line, "full in") > strings.Index(line, "/dev/sdb1") {
		t.Errorf("the fill time came after the device:\n%s", line)
	}
}

// Nothing is said about a disk that is not filling, nor on too little
// history, nor past the reach of the evidence.
func TestAFillTimeIsSaidOnlyOnEvidence(t *testing.T) {
	cases := map[string]*Model{
		"steady":            filling(20, 0),
		"shrinking":         filling(20, ^uint64(0)-4_999), // wraps to minus five thousand
		"five samples":      filling(5, 50_000),
		"beyond its reach":  filling(20, 250), // five and a half hours from ninety seconds
		"within df's noise": filling(20, 40),
	}
	for name, m := range cases {
		if line := fillText(m); strings.Contains(line, "full in") {
			t.Errorf("%s: a fill time was said:\n%s", name, line)
		}
	}
}

func TestFormatETA(t *testing.T) {
	cases := map[time.Duration]string{
		20 * time.Second:                "~1m",
		14*time.Minute + 40*time.Second: "~15m",
		90 * time.Minute:                "~2h",
		5 * time.Hour:                   "~5h",
	}
	for eta, want := range cases {
		if got := formatETA(eta); got != want {
			t.Errorf("formatETA(%v) = %q, want %q", eta, got, want)
		}
	}
}

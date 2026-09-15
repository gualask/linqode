package status

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/spark"
)

// While the stream runs the panel says so, which is the screen's cue to stop
// paying two seconds for a staler answer to the same question.
func TestLiveStreamFeedsTheColumnsAndSaysSo(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	stream := openLive(t, m)
	if !m.LiveActive() {
		t.Error("the panel did not report the stream as running")
	}
	stream.events <- liveReading("web", "12.34%", "153.6MiB")
	m.Update(statsTickMsg{})
	if !strings.Contains(m.View(), "12.34%") {
		t.Errorf("live samples did not reach the columns:\n%s", m.View())
	}
	m.Update(key("a"))
	if m.LiveActive() {
		t.Error("the panel still reports a stream it closed")
	}
}

func TestClosingLiveKeepsReadingsOnlyWhenPolled(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	stream := openLive(t, m)
	stream.events <- liveReading("web", "12.34%", "153.6MiB")
	m.Update(statsTickMsg{})
	m.Update(key("a"))
	if !stream.stopped {
		t.Error("closing the panel left the remote docker stats running")
	}
	if !strings.Contains(m.View(), "12.34%") {
		t.Errorf("readings dropped although the soft poll owns them:\n%s", m.View())
	}
	unpolled := New(Config{})
	unpolled.liveStats = true
	unpolled.SetSize(120, 24)
	unpolled.SetServices(services("web"), nil)
	stream = openLive(t, unpolled)
	stream.events <- liveReading("web", "12.34%", "153.6MiB")
	unpolled.Update(statsTickMsg{})
	unpolled.Update(key("a"))
	view := unpolled.View()
	if strings.Contains(view, "12.34%") || strings.Contains(view, "CPU") {
		t.Errorf("stale live readings survived without a poller:\n%s", view)
	}
	if unpolled.trends != nil {
		t.Errorf("a trend nothing will extend survived the stream: %v", unpolled.trends)
	}
}

// hasHint reports whether the panel currently publishes a hint containing
// text, which is what the screen would join into the footer.
func hasHint(m *Model, text string) bool {
	for _, hint := range m.Hints() {
		if strings.Contains(hint.Text, text) {
			return true
		}
	}
	return false
}

func TestLiveStatsUnavailableWithoutCapability(t *testing.T) {
	m := New(Config{})
	m.SetSize(120, 24)
	if cmd := m.Update(key("a")); cmd != nil {
		t.Fatal("`a` started live stats without the configured capability")
	}
	if hasHint(m, "a live") {
		t.Fatalf("panel advertised unavailable live stats: %+v", m.Hints())
	}
}

func TestLiveToggleWhileStartingCancels(t *testing.T) {
	m := withStatsFetch()
	cmd := m.Update(key("a"))
	if cmd == nil {
		t.Fatal("first press did not start")
	}
	if cmd := m.Update(key("a")); cmd != nil {
		t.Error("second press should cancel, not start another stream")
	}
	if m.statsStarting {
		t.Error("still marked as starting after cancelling")
	}
	request := cmd().(OpenStatsMsg)
	stream := newStatsStream()
	if cmd := m.Update(StatsFeedMsg{RequestID: request.RequestID, Feed: stream.feed()}); cmd != nil {
		t.Error("cancelled request started ticking")
	}
	if m.LiveActive() || !stream.stopped {
		t.Fatal("late response reopened the cancelled stream")
	}
}

func TestSupersededLiveResponseCannotReplaceNewRequest(t *testing.T) {
	for _, oldFirst := range []bool{true, false} {
		m := withStatsFetch()
		old := m.Update(key("a"))().(OpenStatsMsg)
		m.Update(key("a"))
		current := m.Update(key("a"))().(OpenStatsMsg)
		oldStream, currentStream := newStatsStream(), newStatsStream()
		oldMsg := StatsFeedMsg{RequestID: old.RequestID, Feed: oldStream.feed()}
		currentMsg := StatsFeedMsg{RequestID: current.RequestID, Feed: currentStream.feed()}
		if oldFirst {
			m.Update(oldMsg)
			if !m.statsStarting {
				t.Fatal("obsolete response cleared the newer pending request")
			}
			m.Update(currentMsg)
		} else {
			m.Update(currentMsg)
			m.Update(oldMsg)
		}
		m.Update(StatsFeedMsg{RequestID: old.RequestID, Err: errors.New("obsolete failure")})
		if !oldStream.stopped || currentStream.stopped || m.statsFeed.Events != currentStream.events || m.statsErr != "" {
			t.Fatalf("obsolete response affected the new stream (old response first: %v)", oldFirst)
		}
		m.stopLive()
	}
}

func TestLiveStreamEndingStopsTicking(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	stream := openLive(t, m)
	stream.events <- liveReading("web", "12.34%", "153.6MiB")
	stream.events <- operations.Event{Kind: operations.EventExit, ExitCode: 0}
	cmd := m.Update(statsTickMsg{})
	if !stream.stopped {
		t.Error("ended stream was not cleaned up")
	}
	if cmd != nil {
		if _, ticking := cmd().(statsTickMsg); ticking {
			t.Error("kept ticking after the stream ended")
		}
	}
	if !strings.Contains(m.View(), "12.34%") || len(m.trends["app-web-1"]) == 0 {
		t.Errorf("ended stream state was not retained correctly:\n%s", m.View())
	}
}

func TestLiveFailureReported(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	request := m.Update(key("a"))().(OpenStatsMsg)
	m.Update(StatsFeedMsg{RequestID: request.RequestID, Err: errors.New("permission denied")})
	if m.statsStarting || !strings.Contains(m.Status(), "permission denied") {
		t.Errorf("live failure not reported: %q", m.Status())
	}
}

func TestLiveStderrSurfacedWithoutStopping(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	stream := openLive(t, m)
	stream.events <- operations.Event{Kind: operations.EventStderr, Text: "cannot read stats for app-web-1"}
	if cmd := m.Update(statsTickMsg{}); cmd == nil {
		t.Error("a stderr line should not stop the stream")
	}
	if !strings.Contains(m.Status(), "cannot read stats") {
		t.Errorf("stderr not surfaced: %q", m.Status())
	}
}

func TestStatsTickWithoutStreamIsInert(t *testing.T) {
	m := New(Config{})
	if cmd := m.Update(statsTickMsg{}); cmd != nil {
		t.Error("a stray tick scheduled another with no stream running")
	}
}

func TestLivePanelShowsSeriesPerContainer(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	if strings.Contains(m.View(), "live ·") {
		t.Errorf("live panel on screen before it was opened:\n%s", m.View())
	}
	stream := openLive(t, m)
	stream.events <- liveReading("web", "12.34%", "153.6MiB")
	stream.events <- liveReading("web", "6.00%", "150MiB")
	m.Update(statsTickMsg{})
	view := m.View()
	for _, text := range []string{"live · 1s", "peak  12.3%"} {
		if !strings.Contains(view, text) {
			t.Errorf("%q missing from panel:\n%s", text, view)
		}
	}
	// The hint flips to say what the key now does; the screen is what puts
	// it on the footer.
	if !hasHint(m, "a live off") {
		t.Errorf("hint did not flip while the stream is open: %+v", m.Hints())
	}
	if !strings.ContainsAny(view, spark.Glyphs) {
		t.Errorf("no sparkline drawn:\n%s", view)
	}
}

func TestLivePanelAnnouncesStartup(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web"), nil)
	m.Update(key("a"))
	if !strings.Contains(m.View(), "starting") {
		t.Errorf("startup not announced:\n%s", m.View())
	}
}

func TestLivePanelLeavesTheTableOnScreen(t *testing.T) {
	m := withStatsFetch()
	m.SetSize(120, 20)
	m.SetServices(services("web", "db", "cache"), nil)
	stream := openLive(t, m)
	for _, name := range []string{"web", "db", "cache"} {
		stream.events <- liveReading(name, "12.34%", "153.6MiB")
	}
	m.Update(statsTickMsg{})
	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines > 20 {
		t.Errorf("view is %d lines, taller than terminal:\n%s", lines, view)
	}
	for index, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 120 {
			t.Errorf("line %d is %d columns wide: %q", index, width, line)
		}
	}
	if !strings.Contains(view, "SERVICE") {
		t.Errorf("table heading missing from constrained view:\n%s", view)
	}
	// Nothing is wrong, so the panel's half of the footer says nothing: the
	// service counts live on its own rule now.
	if status := m.Status(); status != "" {
		t.Errorf("panel status = %q, want nothing while all is well", status)
	}
}

// A strip keeps its width, starting beside its number; it is not clamped at a
// hundred, since two busy cores are not one; and an idle container's jitter is
// not drawn as a swing.
func TestTrendStrip(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cpu := func(values ...float64) []point {
		points := make([]point, len(values))
		for i, value := range values {
			points[i] = point{at: at.Add(time.Duration(i) * 5 * time.Second), cpu: value, hasCPU: true}
		}
		return points
	}
	if got, _ := trendStrip(cpu(1), 4, cpuReading, cpuScale); len([]rune(got)) != 4 || !strings.HasSuffix(got, "   ") {
		t.Errorf("short series not left-aligned at its width: %q", got)
	}
	got, seconds := trendStrip(cpu(9, 9, 9, 1), 2, cpuReading, cpuScale)
	if len([]rune(got)) != 2 || seconds != 5 {
		t.Errorf("strip %q covering %vs, want two cells over the last 5s", got, seconds)
	}
	if got, _ := trendStrip(nil, 3, cpuReading, cpuScale); got != "   " {
		t.Errorf("empty series drew %q", got)
	}
	if got, _ := trendStrip(cpu(1, 2), 0, cpuReading, cpuScale); got != "" {
		t.Errorf("zero width drew %q", got)
	}
	if got, _ := trendStrip(cpu(150, 250), 2, cpuReading, cpuScale); []rune(got)[1] != '█' || []rune(got)[0] == '█' {
		t.Errorf("a climb past a hundred drew %q", got)
	}
	if got, _ := trendStrip(cpu(0.1, 0.4), 2, cpuReading, cpuScale); []rune(got)[1] == '█' {
		t.Errorf("an idle jitter drew %q", got)
	}
	// A point without the reading is skipped, not drawn as zero.
	mixed := []point{{at: at, hasMemory: true}, {at: at, cpu: 50, hasCPU: true}}
	if got, _ := trendStrip(mixed, 2, cpuReading, cpuScale); !strings.HasSuffix(got, " ") {
		t.Errorf("a point with no CPU was drawn: %q", got)
	}
}

// ticking is a clock that moves on by step every time it is read.
func ticking(step time.Duration) func() time.Time {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		now = now.Add(step)
		return now
	}
}

// The sampled counters feed the trend while the panel is closed, so it opens
// on minutes of shape rather than on an empty strip — a leak is a slow climb.
func TestSampledReadingsGiveTheLivePanelAHistory(t *testing.T) {
	m := withStatsFetch()
	m.now = ticking(5 * time.Second)
	m.SetServices(services("web"), nil)
	for round := range 24 {
		m.SetStats([]compose.ContainerStats{{
			Name:     "app-web-1",
			CPUPerc:  "12.00%",
			MemUsage: fmt.Sprintf("%dMiB / 31.31GiB", 400+round*4),
			MemPerc:  "1.30%",
		}}, nil)
	}
	openLive(t, m)
	view := m.View()
	if !strings.Contains(view, "live · 1s · 2m") {
		t.Errorf("the panel does not say its strips reach back two minutes:\n%s", view)
	}
	var row string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "peak") {
			row = line
		}
	}
	// The CPU never moved, so its strip is flat at the middle of its window;
	// memory climbed, so its strip ends at the top.
	if !strings.Contains(row, strings.Repeat("▅", 8)) || !strings.HasSuffix(strings.TrimRight(strings.SplitN(row, "peak", 2)[0], " "), "█") {
		t.Errorf("flat CPU beside climbing memory not drawn so:\n%s", row)
	}
}

// A trend is kept for ten minutes and no more than trendDepth samples.
func TestTrendsAreBoundedByAgeAndCount(t *testing.T) {
	m := withStatsFetch()
	m.now = ticking(time.Second)
	for range trendDepth + 50 {
		m.record(compose.ContainerStats{Name: "app-web-1", CPUPerc: "1.00%"})
	}
	if got := len(m.trends["app-web-1"]); got != trendDepth {
		t.Errorf("trend holds %d samples, want %d", got, trendDepth)
	}
	m.now = ticking(time.Minute)
	for range 30 {
		m.record(compose.ContainerStats{Name: "app-web-1", CPUPerc: "1.00%"})
	}
	series := m.trends["app-web-1"]
	if span := series[len(series)-1].at.Sub(series[0].at); span > trendAge {
		t.Errorf("trend reaches back %v, more than %v", span, trendAge)
	}
}

// A container the project no longer has takes its trend with it.
func TestTrendsForgetContainersThatLeft(t *testing.T) {
	m := withStatsFetch()
	m.SetServices(services("web", "db"), nil)
	m.SetStats([]compose.ContainerStats{
		{Name: "app-web-1", CPUPerc: "1.00%"}, {Name: "app-db-1", CPUPerc: "2.00%"}}, nil)
	m.SetServices(services("web"), nil)
	if _, kept := m.trends["app-db-1"]; kept {
		t.Error("the departed container's trend was kept")
	}
	if len(m.trends["app-web-1"]) != 1 {
		t.Error("the remaining container's trend was lost")
	}
}

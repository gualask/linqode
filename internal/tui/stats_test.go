package tui

// Tests for the two resource modes: the periodic soft sample behind the
// table's CPU and MEM columns, and the live stream behind the panel below
// it — including that the two never run at once.

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
)

const webStatsLine = `{"Name":"app-web-1","CPUPerc":"12.34%","MemUsage":"153.6MiB / 2GiB","MemPerc":"7.50%"}`

// sample builds one soft sample from readings.
func sample(readings ...compose.ContainerStats) []compose.ContainerStats {
	return readings
}

// reading is one container's numbers, named to match services().
func reading(service, cpu, mem string) compose.ContainerStats {
	return compose.ContainerStats{
		Name:     "app-" + service + "-1",
		CPUPerc:  cpu,
		MemUsage: mem + " / 2GiB",
		MemPerc:  "7.50%",
	}
}

// withStatsFetch returns a model with the soft sample enabled. The function
// itself is never called: these tests deliver samples directly.
func withStatsFetch() statusModel {
	m := newStatusModel(Info{ComposeDir: "/srv/app"}, nil)
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.setSize(120, 24)
	return m
}

// statsStream is a hand-built feed standing in for the remote command.
type statsStream struct {
	events  chan LogEvent
	stopped bool
}

func newStatsStream() *statsStream {
	return &statsStream{events: make(chan LogEvent, 16)}
}

func (s *statsStream) feed() LogFeed {
	return LogFeed{Events: s.events, Stop: func() { s.stopped = true }}
}

// openLive drives the model through the toggle → feed handshake.
func openLive(t *testing.T, m *statusModel) *statsStream {
	t.Helper()
	cmd := m.update(key("a"))
	if cmd == nil {
		t.Fatal("`a` did not start the live stream")
	}
	open, ok := cmd().(openStatsMsg)
	if !ok {
		t.Fatalf("`a` produced %T, want openStatsMsg", cmd())
	}
	if !strings.Contains(open.command, "docker stats") {
		t.Errorf("live command is not docker stats: %s", open.command)
	}
	if strings.Contains(open.command, "--no-stream") {
		t.Errorf("live mode must stream, not sample once: %s", open.command)
	}
	stream := newStatsStream()
	m.update(statsFeedMsg{feed: stream.feed()})
	return stream
}

// The columns are part of the table from the start when something can fill
// them: a service with no reading yet shows a placeholder, rather than the
// table growing a column later.
func TestStatsColumnsPresentBeforeFirstSample(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web", "db")})

	view := m.view()
	if !strings.Contains(view, "CPU") || !strings.Contains(view, "MEM") {
		t.Errorf("resource columns missing before the first sample:\n%s", view)
	}

	// Without a fetch configured there is nothing to fill them, and they
	// stay out of the table entirely.
	off := newStatusModel(Info{}, nil)
	off.setSize(120, 24)
	off.update(servicesMsg{services: services("web")})
	if strings.Contains(off.view(), "CPU") {
		t.Errorf("resource columns present with no way to fill them:\n%s", off.view())
	}
}

func TestSoftSampleFillsColumns(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web", "db")})
	m.update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	view := m.view()
	if !strings.Contains(view, "12.34%") {
		t.Errorf("CPU reading not shown:\n%s", view)
	}
	// Only the used half of "153.6MiB / 2GiB": the limit repeats on every row.
	if !strings.Contains(view, "153.6MiB") {
		t.Errorf("memory reading not shown:\n%s", view)
	}
	if strings.Contains(view, "2GiB") {
		t.Errorf("memory limit should not take table width:\n%s", view)
	}
	// db has no reading yet, and must still render a row.
	if !strings.Contains(view, "db") {
		t.Errorf("unsampled service dropped from the table:\n%s", view)
	}
}

// A whole sample replaces the previous one: a container missing from it has
// stopped, and its last numbers must not linger as if they were current.
func TestSoftSampleReplacesPreviousReadings(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web", "db")})
	m.update(statsSampleMsg{stats: sample(
		reading("web", "12.34%", "153.6MiB"),
		reading("db", "3.20%", "64MiB"),
	)})
	m.update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	if strings.Contains(m.view(), "3.20%") {
		t.Errorf("reading of a vanished container survived the next sample:\n%s", m.view())
	}
}

// A failed sample keeps the last readings, like a failed host sample keeps
// the header: a blip is not worth blanking the columns for.
func TestFailedSoftSampleKeepsReadings(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	m.update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})
	m.update(statsSampleMsg{err: errors.New("cannot connect to the docker daemon")})

	view := m.view()
	if !strings.Contains(view, "12.34%") {
		t.Errorf("readings dropped after a failed sample:\n%s", view)
	}
	if !strings.Contains(view, "cannot connect") {
		t.Errorf("sample failure not reported:\n%s", view)
	}

	m.update(statsSampleMsg{stats: sample(reading("web", "1.00%", "150MiB"))})
	if strings.Contains(m.view(), "cannot connect") {
		t.Error("error survived a good sample")
	}
}

func TestSoftSampleDoesNotOverlap(t *testing.T) {
	m := withStatsFetch()
	if cmd := m.refreshStats(); cmd == nil {
		t.Fatal("first sample did not start")
	}
	if cmd := m.refreshStats(); cmd != nil {
		t.Error("second sample started while one was in flight")
	}
	m.update(statsSampleMsg{stats: nil})
	if cmd := m.refreshStats(); cmd == nil {
		t.Error("sampling did not resume after the previous one landed")
	}
}

func TestStatsPollTickKeepsTicking(t *testing.T) {
	m := withStatsFetch()
	if cmd := m.update(statsPollMsg{}); cmd == nil {
		t.Error("the poll tick did not schedule the next one")
	}
	// With no fetch configured the tick still reschedules; only the sample
	// is skipped.
	off := newStatusModel(Info{}, nil)
	if cmd := off.update(statsPollMsg{}); cmd == nil {
		t.Error("the poll tick stopped when no sample is configured")
	}
}

// The stream feeds the same columns, faster: paying ~2 s of server time for
// a soft sample while it runs would be pure waste.
func TestSoftPollStandsDownWhileLive(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	stream := openLive(t, &m)

	if cmd := m.refreshStats(); cmd != nil {
		t.Error("a soft sample started while the live stream was running")
	}
	stream.events <- LogEvent{Kind: LogLine, Text: webStatsLine}
	m.update(statsTickMsg{})
	if !strings.Contains(m.view(), "12.34%") {
		t.Errorf("live samples did not reach the columns:\n%s", m.view())
	}

	m.update(key("a")) // close
	if cmd := m.refreshStats(); cmd == nil {
		t.Error("the soft poll did not resume after the stream closed")
	}
}

// Closing the live panel keeps the readings when something refreshes them,
// and drops them when nothing does — stale numbers read as live.
func TestClosingLiveKeepsReadingsOnlyWhenPolled(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	stream := openLive(t, &m)
	stream.events <- LogEvent{Kind: LogLine, Text: webStatsLine}
	m.update(statsTickMsg{})
	m.update(key("a"))

	if !stream.stopped {
		t.Error("closing the panel left the remote docker stats running")
	}
	if !strings.Contains(m.view(), "12.34%") {
		t.Errorf("readings dropped although the soft poll owns them:\n%s", m.view())
	}

	unpolled := newStatusModel(Info{ComposeDir: "/srv/app"}, nil)
	unpolled.setSize(120, 24)
	unpolled.update(servicesMsg{services: services("web")})
	stream = openLive(t, &unpolled)
	stream.events <- LogEvent{Kind: LogLine, Text: webStatsLine}
	unpolled.update(statsTickMsg{})
	unpolled.update(key("a"))

	view := unpolled.view()
	if strings.Contains(view, "12.34%") {
		t.Errorf("stale readings left on screen with nothing to refresh them:\n%s", view)
	}
	if strings.Contains(view, "CPU") {
		t.Errorf("resource columns survived with nothing to fill them:\n%s", view)
	}
}

// Pressing `a` twice quickly must not leave a stream running unattended.
func TestLiveToggleWhileStartingCancels(t *testing.T) {
	m := withStatsFetch()
	if cmd := m.update(key("a")); cmd == nil {
		t.Fatal("first press did not start")
	}
	if cmd := m.update(key("a")); cmd != nil {
		t.Error("second press should cancel, not start another stream")
	}
	if m.statsStarting {
		t.Error("still marked as starting after cancelling")
	}
}

func TestLiveStreamEndingStopsTicking(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	stream := openLive(t, &m)

	stream.events <- LogEvent{Kind: LogLine, Text: webStatsLine}
	stream.events <- LogEvent{Kind: LogEnded, ExitCode: 0}
	cmd := m.update(statsTickMsg{})
	if !stream.stopped {
		t.Error("ended stream was not cleaned up")
	}
	// Handing the columns back to the soft poll is fine; draining a stream
	// that will never produce again is not.
	if cmd != nil {
		if _, ticking := cmd().(statsTickMsg); ticking {
			t.Error("kept ticking after the stream ended")
		}
	}
	// The last samples stay readable even though the stream is gone.
	if !strings.Contains(m.view(), "12.34%") {
		t.Errorf("last samples dropped when the stream ended:\n%s", m.view())
	}
	if m.history != nil {
		t.Error("history outlived the stream that produced it")
	}
}

func TestLiveFailureReported(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	m.update(key("a"))
	m.update(statsFeedMsg{err: errors.New("permission denied")})

	if m.statsStarting {
		t.Error("still marked as starting after a failure")
	}
	if !strings.Contains(m.view(), "permission denied") {
		t.Errorf("live failure not reported:\n%s", m.view())
	}
}

// docker writes complaints to stderr while streaming; the panel surfaces
// them without tearing anything down.
func TestLiveStderrSurfacedWithoutStopping(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	stream := openLive(t, &m)

	stream.events <- LogEvent{Kind: LogStderrLine, Text: "cannot read stats for app-web-1"}
	cmd := m.update(statsTickMsg{})

	if cmd == nil {
		t.Error("a stderr line should not stop the stream")
	}
	if !strings.Contains(m.view(), "cannot read stats") {
		t.Errorf("stderr not surfaced:\n%s", m.view())
	}
}

func TestStatsTickWithoutStreamIsInert(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	if cmd := m.update(statsTickMsg{}); cmd != nil {
		t.Error("a stray tick scheduled another with no stream running")
	}
}

// The panel is the point of the live mode: a series per container, under
// the table, with the scale it is drawn against.
func TestLivePanelShowsSeriesPerContainer(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})

	if strings.Contains(m.view(), "samples") {
		t.Errorf("live panel on screen before it was opened:\n%s", m.view())
	}

	stream := openLive(t, &m)
	stream.events <- LogEvent{Kind: LogLine, Text: webStatsLine}
	stream.events <- LogEvent{Kind: LogLine,
		Text: `{"Name":"app-web-1","CPUPerc":"6.00%","MemUsage":"150MiB / 2GiB","MemPerc":"7.00%"}`}
	m.update(statsTickMsg{})

	view := m.view()
	if !strings.Contains(view, "2 samples") {
		t.Errorf("sample count missing from the panel:\n%s", view)
	}
	if !strings.ContainsAny(view, string(sparkRunes)) {
		t.Errorf("no sparkline drawn:\n%s", view)
	}
	// The sparkline is scaled to its own peak, so the peak is stated.
	if !strings.Contains(view, "peak  12.3%") {
		t.Errorf("peak missing from the panel:\n%s", view)
	}
	// The footer says how to get out again.
	if !strings.Contains(view, "a live off") {
		t.Errorf("footer does not offer closing the panel:\n%s", view)
	}
}

// While docker takes its couple of seconds to produce the first block, the
// panel says so: silence would read as a hang.
func TestLivePanelAnnouncesStartup(t *testing.T) {
	m := withStatsFetch()
	m.update(servicesMsg{services: services("web")})
	m.update(key("a"))

	if !strings.Contains(m.view(), "starting") {
		t.Errorf("startup not announced:\n%s", m.view())
	}
}

// The panel takes its space from the table, never from the footer, and
// never past the right edge: a row that wraps shifts everything below it.
func TestLivePanelLeavesTableAndFooterOnScreen(t *testing.T) {
	m := withStatsFetch()
	m.hostFetch = func() (host.Metrics, error) { return host.Metrics{}, nil }
	m.setSize(120, 20)
	m.update(servicesMsg{services: services("web", "db", "cache")})
	m.update(hostMsg{metrics: sampleMetrics()})
	stream := openLive(t, &m)
	for _, name := range []string{"web", "db", "cache"} {
		stream.events <- LogEvent{Kind: LogLine,
			Text: `{"Name":"app-` + name + `-1","CPUPerc":"12.34%","MemUsage":"153.6MiB / 2GiB","MemPerc":"7.50%"}`}
	}
	m.update(statsTickMsg{})

	view := m.view()
	if lines := strings.Count(view, "\n") + 1; lines > 20 {
		t.Errorf("view is %d lines, taller than the 20-line terminal:\n%s", lines, view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 120 {
			t.Errorf("line %d is %d columns wide, terminal is 120: %q", i, width, line)
		}
	}
	if !strings.Contains(view, "SERVICE") {
		t.Errorf("table header pushed off screen:\n%s", view)
	}
	if !strings.Contains(view, "3 services") {
		t.Errorf("footer pushed off screen:\n%s", view)
	}
}

func TestSparkline(t *testing.T) {
	// Scaled to the window's peak: the largest value is the tallest rune,
	// the smallest is not.
	line := sparkline([]float64{1, 2, 4}, 3)
	if got, want := []rune(line)[2], sparkRunes[len(sparkRunes)-1]; got != want {
		t.Errorf("peak drawn as %q, want %q (line %q)", got, want, line)
	}
	if got := []rune(line)[0]; got == sparkRunes[len(sparkRunes)-1] {
		t.Errorf("smallest value drawn at full height: %q", line)
	}

	// Shorter series are right-aligned, so the newest sample stays put.
	if got := sparkline([]float64{1}, 4); !strings.HasPrefix(got, "   ") {
		t.Errorf("short series not right-aligned: %q", got)
	}
	// Longer ones keep their tail.
	if got := sparkline([]float64{9, 9, 9, 1}, 2); len([]rune(got)) != 2 {
		t.Errorf("sparkline wider than asked: %q", got)
	}
	// Degenerate inputs draw nothing rather than panicking.
	if got := sparkline(nil, 3); got != "   " {
		t.Errorf("empty series drew %q", got)
	}
	if got := sparkline([]float64{1, 2}, 0); got != "" {
		t.Errorf("zero width drew %q", got)
	}
	// An all-zero series is flat at the baseline, not divided by zero.
	if got := sparkline([]float64{0, 0}, 2); got != string([]rune{sparkRunes[0], sparkRunes[0]}) {
		t.Errorf("idle series drew %q", got)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	m := withStatsFetch()
	for range historyLen + 50 {
		m.record(compose.ContainerStats{Name: "app-web-1", CPUPerc: "1.00%"})
	}
	if got := len(m.history["app-web-1"]); got != historyLen {
		t.Errorf("history holds %d samples, want %d", got, historyLen)
	}
}

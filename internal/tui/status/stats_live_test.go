package status

import (
	"errors"
	"strings"
	"testing"

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
	if !strings.Contains(m.View(), "12.34%") || m.history != nil {
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
	if strings.Contains(m.View(), "samples") {
		t.Errorf("live panel on screen before it was opened:\n%s", m.View())
	}
	stream := openLive(t, m)
	stream.events <- liveReading("web", "12.34%", "153.6MiB")
	stream.events <- liveReading("web", "6.00%", "150MiB")
	m.Update(statsTickMsg{})
	view := m.View()
	for _, text := range []string{"2 samples", "peak  12.3%"} {
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

// The live strip is right-aligned at a fixed width, so the right-hand end
// means "just now" on every row, and it is not clamped at a hundred: two busy
// cores are not drawn as one.
func TestLiveStrip(t *testing.T) {
	if got := liveStrip([]float64{1}, 4); len([]rune(got)) != 4 || !strings.HasPrefix(got, "   ") {
		t.Errorf("short series not right-aligned at its width: %q", got)
	}
	if got := liveStrip([]float64{9, 9, 9, 1}, 2); len([]rune(got)) != 2 {
		t.Errorf("strip wider than asked: %q", got)
	}
	if got := liveStrip(nil, 3); got != "   " {
		t.Errorf("empty series drew %q", got)
	}
	if got := liveStrip([]float64{1, 2}, 0); got != "" {
		t.Errorf("zero width drew %q", got)
	}
	if got := []rune(liveStrip([]float64{150, 250}, 2)); got[1] != '█' || got[0] == '█' {
		t.Errorf("a climb past a hundred drew %q", string(got))
	}
	// An idle container jittering by a fraction of a point is not a swing.
	if got := []rune(liveStrip([]float64{0.1, 0.4}, 2)); got[1] == '█' {
		t.Errorf("an idle jitter drew %q", string(got))
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

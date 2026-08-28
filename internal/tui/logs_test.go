package tui

// Unit tests for the log view model, fed through a hand-built channel —
// no SSH involved.

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/operations"
)

// feedOf builds a feed whose channel already holds events.
func feedOf(events ...operations.Event) (operations.Feed, chan operations.Event) {
	ch := make(chan operations.Event, len(events)+16)
	for _, ev := range events {
		ch <- ev
	}
	return operations.Feed{Events: ch, Stop: func() {}}, ch
}

func lineEvents(lines ...string) []operations.Event {
	events := make([]operations.Event, len(lines))
	for i, line := range lines {
		events[i] = operations.Event{Kind: operations.EventLog, Text: line}
	}
	return events
}

func newTestLogView(events ...operations.Event) *logsModel {
	feed, _ := feedOf(events...)
	m := newLogsModel("deploy@prod", "logs: web", feed)
	m.setSize(80, 12) // viewport 10
	m.drain()
	return &m
}

func TestDrainAppliesLinesAndFollowTracksTail(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m := newTestLogView(lineEvents(lines...)...)

	view := m.view()
	if !strings.Contains(view, "line 29") {
		t.Errorf("following view misses the tail:\n%s", view)
	}
	if !strings.Contains(view, "following") {
		t.Errorf("follow indicator missing:\n%s", view)
	}
	if !strings.Contains(view, "30 lines") {
		t.Errorf("line count missing:\n%s", view)
	}
}

func TestScrollUpLeavesFollowAndBottomReenters(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m := newTestLogView(lineEvents(lines...)...)
	m.view() // computes scroll = max

	m.update(key("k"))
	if m.follow {
		t.Error("scrolling up should leave follow mode")
	}
	m.update(key("j"))
	if !m.follow {
		t.Error("hitting bottom should re-enter follow mode")
	}
	m.update(key("g"))
	if m.follow || m.scroll != 0 {
		t.Errorf("g: follow=%v scroll=%d", m.follow, m.scroll)
	}
	m.update(key("G"))
	if !m.follow {
		t.Error("G should re-enter follow mode")
	}
}

func TestSearchJumpsAndCyclesMatches(t *testing.T) {
	m := newTestLogView(lineEvents(
		"alpha one", "noise", "alpha two", "noise", "ALPHA three")...)

	m.update(key("/"))
	for _, r := range "alpha" {
		m.update(key(string(r)))
	}
	m.update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.matchLine != 0 {
		t.Errorf("first match at %d, want 0", m.matchLine)
	}
	m.update(key("n"))
	if m.matchLine != 2 {
		t.Errorf("second match at %d, want 2", m.matchLine)
	}
	m.update(key("n"))
	if m.matchLine != 4 { // case-insensitive
		t.Errorf("third match at %d, want 4", m.matchLine)
	}
	m.update(key("n"))
	if m.matchLine != 0 { // wrapped
		t.Errorf("wrapped match at %d, want 0", m.matchLine)
	}
	m.update(key("N"))
	if m.matchLine != 4 { // wrapped backwards
		t.Errorf("backwards match at %d, want 4", m.matchLine)
	}

	if !strings.Contains(m.view(), "/alpha") {
		t.Error("committed query missing from footer")
	}
}

func TestSearchWithoutMatchShowsNotice(t *testing.T) {
	m := newTestLogView(lineEvents("alpha", "beta")...)
	m.update(key("/"))
	m.update(key("x"))
	m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.view(), "no match") {
		t.Errorf("notice missing:\n%s", m.view())
	}
}

func TestEndedStreamShowsExitAndStderr(t *testing.T) {
	events := append(lineEvents("bye"),
		operations.Event{Kind: operations.EventStderr, Text: "no such service"},
		operations.Event{Kind: operations.EventExit, ExitCode: 1})
	m := newTestLogView(events...)

	view := m.view()
	if !strings.Contains(view, "exit 1") {
		t.Errorf("exit code missing:\n%s", view)
	}
	if !strings.Contains(view, "no such service") {
		t.Errorf("stderr notice missing:\n%s", view)
	}
}

func TestCloseEmitsCloseFollowMsg(t *testing.T) {
	m := newTestLogView()
	cmd := m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(closeFollowMsg); !ok {
		t.Fatalf("got %T", cmd())
	}
}

func TestBurstDrainIsBounded(t *testing.T) {
	ch := make(chan operations.Event, maxEventsPerTick+200)
	feed := operations.Feed{Events: ch, Stop: func() {}}
	for range maxEventsPerTick + 100 {
		ch <- operations.Event{Kind: operations.EventLog, Text: "x"}
	}
	m := newLogsModel("t", "logs: web", feed)
	m.setSize(80, 12)
	m.drain()
	if got := m.store.Len(); got != maxEventsPerTick {
		t.Errorf("one drain applied %d events, want %d", got, maxEventsPerTick)
	}
	m.drain()
	if got := m.store.Len(); got != maxEventsPerTick+100 {
		t.Errorf("after second drain %d events, want %d", got, maxEventsPerTick+100)
	}
}

func jsonlEvents(n int, level string) []operations.Event {
	events := make([]operations.Event, n)
	for i := range events {
		events[i] = operations.Event{Kind: operations.EventLog,
			Text: fmt.Sprintf(`{"level":"%s","msg":"event %d"}`, level, i)}
	}
	return events
}

func typeText(m *logsModel, text string) {
	for _, r := range text {
		m.update(key(string(r)))
	}
}

func TestFilterNarrowsViewAndFooterShowsCounts(t *testing.T) {
	events := append(jsonlEvents(3, "error"), jsonlEvents(5, "info")...)
	m := newTestLogView(events...)

	m.update(key("f"))
	typeText(m, "level=error")
	m.update(tea.KeyMsg{Type: tea.KeyEnter})

	view := m.view()
	if !strings.Contains(view, "3/8 lines") {
		t.Errorf("filtered counts missing:\n%s", view)
	}
	if !strings.Contains(view, "f:level=error") {
		t.Errorf("filter expression missing:\n%s", view)
	}

	// Clearing the filter restores the whole tail.
	m.update(key("f"))
	for range len("level=error") {
		m.update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.view(), "8 lines") {
		t.Errorf("filter not cleared:\n%s", m.view())
	}
}

func TestBadFilterShowsNotice(t *testing.T) {
	m := newTestLogView(jsonlEvents(2, "info")...)
	m.update(key("f"))
	typeText(m, "oops")
	m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.view(), "bad filter") {
		t.Errorf("notice missing:\n%s", m.view())
	}
	if m.store.Filter() != nil {
		t.Error("bad filter must not be applied")
	}
}

func TestStructuredAutoDetectionAndOverride(t *testing.T) {
	m := newTestLogView(jsonlEvents(4, "info")...)
	if !strings.Contains(m.view(), "json") {
		t.Errorf("auto-detected json marker missing:\n%s", m.view())
	}
	m.update(key("s")) // manual override off
	if strings.Contains(m.view(), "· json") {
		t.Errorf("override ignored:\n%s", m.view())
	}
	m.update(key("s")) // back on
	if !strings.Contains(m.view(), "· json") {
		t.Errorf("override back on ignored:\n%s", m.view())
	}
}

func TestStructuredRenderingShowsLevelAndFields(t *testing.T) {
	m := newTestLogView(lineEvents(
		`{"level":"error","msg":"boom","ts":"12:00:01","http":{"status":500}}`,
		`{"level":"error","msg":"boom again"}`,
		`{"level":"error","msg":"boom thrice"}`)...)
	view := m.view()
	if !strings.Contains(view, "boom") || !strings.Contains(view, "http.status=500") {
		t.Errorf("structured layout missing:\n%s", view)
	}
	if !strings.Contains(view, "12:00:01") {
		t.Errorf("timestamp missing:\n%s", view)
	}
}

func TestStatsPanelShowsLevelsAndTopField(t *testing.T) {
	events := append(jsonlEvents(2, "error"), jsonlEvents(1, "info")...)
	m := newTestLogView(events...)

	m.update(key("a"))
	view := m.view()
	if !strings.Contains(view, "levels") || !strings.Contains(view, "error") {
		t.Errorf("levels missing:\n%s", view)
	}
	if !strings.Contains(view, "3 lines") {
		t.Errorf("totals missing:\n%s", view)
	}

	m.update(key("t"))
	typeText(m, "level")
	m.update(tea.KeyMsg{Type: tea.KeyEnter})
	view = m.view()
	if !strings.Contains(view, "top level") {
		t.Errorf("top field header missing:\n%s", view)
	}
}

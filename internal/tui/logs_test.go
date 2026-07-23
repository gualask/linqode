package tui

// Unit tests for the log view model, fed through a hand-built channel —
// no SSH involved.

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// feedOf builds a LogFeed whose channel already holds events.
func feedOf(events ...LogEvent) (LogFeed, chan LogEvent) {
	ch := make(chan LogEvent, len(events)+16)
	for _, ev := range events {
		ch <- ev
	}
	return LogFeed{Events: ch, Stop: func() {}}, ch
}

func lineEvents(lines ...string) []LogEvent {
	events := make([]LogEvent, len(lines))
	for i, line := range lines {
		events[i] = LogEvent{Kind: LogLine, Text: line}
	}
	return events
}

func newTestLogView(events ...LogEvent) *logsModel {
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
		LogEvent{Kind: LogStderrLine, Text: "no such service"},
		LogEvent{Kind: LogEnded, ExitCode: 1})
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
	ch := make(chan LogEvent, maxEventsPerTick+200)
	feed := LogFeed{Events: ch, Stop: func() {}}
	for range maxEventsPerTick + 100 {
		ch <- LogEvent{Kind: LogLine, Text: "x"}
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

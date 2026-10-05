package follow

// Unit tests for the log view model, fed through a hand-built channel —
// no SSH involved.

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/operations"
)

func key(k string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

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

func newTestModel(events ...operations.Event) *Model {
	feed, _ := feedOf(events...)
	m := New("deploy@prod", "logs: web", feed)
	m.SetSize(80, 12) // viewport 10
	m.drain()
	return m
}

// A drain tick belongs to the view that armed it. One still in flight when its
// view closed used to reach the next view opened and re-arm there beside that
// view's own, so every quick close-and-reopen added a chain for good.
func TestATickFromAClosedViewDoesNotRearmInTheNext(t *testing.T) {
	closed := newTestModel()
	stale := closed.Init()().(drainTickMsg)
	next := newTestModel()
	if cmd := next.Update(stale); cmd != nil {
		t.Error("a tick from a closed view re-armed in the next one")
	}
	if cmd := next.Update(drainTickMsg{view: next.view}); cmd == nil {
		t.Error("a view's own tick did not re-arm")
	}
}

func TestDrainAppliesLinesAndFollowTracksTail(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m := newTestModel(lineEvents(lines...)...)

	view := m.View()
	if !strings.Contains(view, "line 29") {
		t.Errorf("following view misses the tail:\n%s", view)
	}
	if !strings.Contains(view, "following") {
		t.Errorf("follow indicator missing:\n%s", view)
	}
	// The count is the stats panel's, not the footer's.
	if strings.Contains(view, "30 lines") {
		t.Errorf("the footer still counts the lines:\n%s", view)
	}
}

func TestScrollUpLeavesFollowAndBottomReenters(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m := newTestModel(lineEvents(lines...)...)
	m.View() // computes scroll = max

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.follow {
		t.Error("scrolling up should leave follow mode")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if !m.follow {
		t.Error("hitting bottom should re-enter follow mode")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.follow || m.scroll != 0 {
		t.Errorf("g: follow=%v scroll=%d", m.follow, m.scroll)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.follow {
		t.Error("G should re-enter follow mode")
	}
}

func TestSearchJumpsAndCyclesMatches(t *testing.T) {
	m := newTestModel(lineEvents(
		"alpha one", "noise", "alpha two", "noise", "ALPHA three")...)

	m.Update(key("/"))
	for _, r := range "alpha" {
		m.Update(key(string(r)))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Following, the search lands on the most recent match, and N walks
	// back through the older ones.
	if m.matchLine != 4 { // case-insensitive
		t.Errorf("first match at %d, want the newest, 4", m.matchLine)
	}
	m.Update(key("N"))
	if m.matchLine != 2 {
		t.Errorf("older match at %d, want 2", m.matchLine)
	}
	m.Update(key("N"))
	if m.matchLine != 0 {
		t.Errorf("oldest match at %d, want 0", m.matchLine)
	}
	m.Update(key("N"))
	if m.matchLine != 4 { // wrapped backwards
		t.Errorf("wrapped match at %d, want 4", m.matchLine)
	}
	m.Update(key("n"))
	if m.matchLine != 0 { // wrapped forwards
		t.Errorf("wrapped match at %d, want 0", m.matchLine)
	}

	if !strings.Contains(m.View(), "/alpha") {
		t.Error("committed query missing from footer")
	}
}

// Off the tail, a search starts at the cursor, the line being read — not at
// the top of the screen, and n not at wherever the last search stopped.
func TestSearchStartsAtTheCursor(t *testing.T) {
	m := newTestModel(lineEvents("alpha", "noise", "noise", "alpha", "noise", "alpha")...)
	m.SetSize(80, 20)
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp}) // line 3
	m.Update(key("/"))
	typeText(m, "alpha")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.matchLine != 3 {
		t.Fatalf("first match at %d, want the cursor's own line, 3", m.matchLine)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome}) // back to line 0
	m.Update(key("n"))
	if m.matchLine != 3 {
		t.Errorf("n went to %d, want the first match after the cursor, 3", m.matchLine)
	}
}

// A record is searched as it is drawn: a key of the raw JSON is not a hit,
// since no line drawn would show it, and a decoded value is.
func TestStructuredLinesAreSearchedAsDrawn(t *testing.T) {
	m := newTestModel(lineEvents(
		`{"level":"info","msg":"caf\u00e9 open"}`,
		`{"level":"info","msg":"other"}`,
		`{"level":"info","msg":"other"}`)...)
	if !m.structuredRendering() {
		t.Fatal("the lines were not read as records")
	}
	m.Update(key("/"))
	typeText(m, "msg")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.matchLine >= 0 || !strings.Contains(m.View(), "no match") {
		t.Errorf("a raw JSON key was a hit, on line %d", m.matchLine)
	}
	m.Update(key("/"))
	typeText(m, "café")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.matchLine != 0 {
		t.Errorf("the decoded value was not found: match at %d", m.matchLine)
	}
}

func TestSearchWithoutMatchShowsNotice(t *testing.T) {
	m := newTestModel(lineEvents("alpha", "beta")...)
	m.Update(key("/"))
	m.Update(key("x"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "no match") {
		t.Errorf("notice missing:\n%s", m.View())
	}
}

func TestEndedStreamShowsExitAndStderr(t *testing.T) {
	events := append(lineEvents("bye"),
		operations.Event{Kind: operations.EventStderr, Text: "no such service"},
		operations.Event{Kind: operations.EventExit, ExitCode: 1})
	m := newTestModel(events...)

	view := m.View()
	if !strings.Contains(view, "exit 1") {
		t.Errorf("exit code missing:\n%s", view)
	}
	if !strings.Contains(view, "no such service") {
		t.Errorf("stderr notice missing:\n%s", view)
	}
}

func TestCloseEmitsCloseMsg(t *testing.T) {
	m := newTestModel()
	cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(CloseMsg); !ok {
		t.Fatalf("got %T", cmd())
	}
}

func TestBurstDrainIsBounded(t *testing.T) {
	ch := make(chan operations.Event, maxEventsPerTick+200)
	feed := operations.Feed{Events: ch, Stop: func() {}}
	for range maxEventsPerTick + 100 {
		ch <- operations.Event{Kind: operations.EventLog, Text: "x"}
	}
	m := New("t", "logs: web", feed)
	m.SetSize(80, 12)
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

func typeText(m *Model, text string) {
	for _, r := range text {
		m.Update(key(string(r)))
	}
}

func TestFilterNarrowsViewAndFooterShowsIt(t *testing.T) {
	events := append(jsonlEvents(3, "error"), jsonlEvents(5, "info")...)
	m := newTestModel(events...)

	m.Update(key("f"))
	typeText(m, "level=error")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if m.store.Len() != 3 {
		t.Errorf("%d lines in view, want the 3 errors", m.store.Len())
	}
	if !strings.Contains(view, "f:level=error") {
		t.Errorf("filter expression missing:\n%s", view)
	}

	// Clearing the filter restores the whole tail.
	m.Update(key("f"))
	for range len("level=error") {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.store.Len() != 8 || strings.Contains(m.View(), "f:") {
		t.Errorf("filter not cleared:\n%s", m.View())
	}
}

func TestBadFilterShowsNotice(t *testing.T) {
	m := newTestModel(jsonlEvents(2, "info")...)
	m.Update(key("f"))
	typeText(m, "oops")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "bad filter") {
		t.Errorf("notice missing:\n%s", m.View())
	}
	if m.store.Filter() != nil {
		t.Error("bad filter must not be applied")
	}
}

func TestStructuredAutoDetectionAndOverride(t *testing.T) {
	m := newTestModel(jsonlEvents(4, "info")...)
	if !strings.Contains(m.View(), "json") {
		t.Errorf("auto-detected json marker missing:\n%s", m.View())
	}
	m.Update(key("s")) // manual override off
	if strings.Contains(m.View(), "· json") {
		t.Errorf("override ignored:\n%s", m.View())
	}
	m.Update(key("s")) // back on
	if !strings.Contains(m.View(), "· json") {
		t.Errorf("override back on ignored:\n%s", m.View())
	}
}

func TestStructuredRenderingShowsLevelAndFields(t *testing.T) {
	m := newTestModel(lineEvents(
		`{"level":"error","msg":"boom","ts":"12:00:01","http":{"status":500}}`,
		`{"level":"error","msg":"boom again"}`,
		`{"level":"error","msg":"boom thrice"}`)...)
	view := m.View()
	if !strings.Contains(view, "boom") || !strings.Contains(view, "http.status=500") {
		t.Errorf("structured layout missing:\n%s", view)
	}
	if !strings.Contains(view, "12:00:01") {
		t.Errorf("timestamp missing:\n%s", view)
	}
}

func TestStatsPanelShowsLevelsAndTopField(t *testing.T) {
	events := append(jsonlEvents(2, "error"), jsonlEvents(1, "info")...)
	m := newTestModel(events...)
	// The panel is cut to the log's height, and the top field is at its foot.
	m.SetSize(80, 24)

	m.Update(key("a"))
	view := m.View()
	if !strings.Contains(view, "levels") || !strings.Contains(view, "error") {
		t.Errorf("levels missing:\n%s", view)
	}
	if !strings.Contains(view, "3 lines") {
		t.Errorf("totals missing:\n%s", view)
	}
	// Level and message are the well-known fields, and nothing else is left
	// to count by.
	if !strings.Contains(view, "(no fields to count by)") {
		t.Errorf("a field was offered where there is none:\n%s", view)
	}
}

// `esc` goes back to the screen this was opened from; `q` leaves the
// application. They used to do the same thing, which made the footer's
// promise of two different keys wrong about one of them.
func TestEscGoesBackAndQuitLeaves(t *testing.T) {
	feed, _ := feedOf(lineEvents("first line")...)
	m := New("deploy@prod", "logs: web", feed)
	m.SetSize(100, 24)

	cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc did nothing")
	}
	if _, closing := cmd().(CloseMsg); !closing {
		t.Errorf("esc did not ask to close the view: %T", cmd())
	}

	cmd = m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q did nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Errorf("q did not quit: %T", cmd())
	}

	// And the footer says both.
	view := m.View()
	for _, want := range []string{"esc back", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("the footer does not offer %q:\n%s", want, view)
		}
	}
}

// While a search or a filter is being typed, `q` is a character. The way out
// of the input is `esc`, which cancels it rather than the view.
func TestQTypesWhileSearching(t *testing.T) {
	feed, _ := feedOf(lineEvents("first line")...)
	m := New("deploy@prod", "logs: web", feed)
	m.SetSize(100, 24)

	m.Update(key("/"))
	for _, k := range []string{"q", "u", "e"} {
		if cmd := m.Update(key(k)); cmd != nil {
			t.Fatalf("key %q acted while typing", k)
		}
	}
	if m.inputText != "que" {
		t.Errorf("typed text %q", m.inputText)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.input != inputNone {
		t.Error("esc did not cancel the search input")
	}
}

// A notice answers one key and goes with the next: `x` after a search that
// found nothing brings the keys back, not the stale "no match".
func TestANoticeGoesWithTheNextKey(t *testing.T) {
	m := newTestModel(lineEvents("alpha", "beta")...)
	m.SetSize(80, 10)
	m.Update(key("/"))
	typeText(m, "zzz")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "no match") {
		t.Fatalf("no notice for a search that found nothing:\n%s", m.View())
	}
	m.Update(key("x"))
	if view := m.View(); strings.Contains(view, "no match") || !strings.Contains(view, "q quit") {
		t.Errorf("the notice outlived the search x cleared:\n%s", view)
	}
}

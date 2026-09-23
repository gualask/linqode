package follow

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/operations"
)

// statsModel is a log view whose clock is noon, holding lines written over
// the last ten minutes, with the stats panel open at the given height.
func statsModel(t *testing.T, height int, lines ...string) *Model {
	t.Helper()
	noon := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	events := make([]operations.Event, len(lines))
	for i, line := range lines {
		events[i] = operations.Event{Kind: operations.EventLog, Text: line}
	}
	feed, _ := feedOf(events...)
	m := New("deploy@prod", "logs: web", feed)
	m.now = func() time.Time { return noon }
	m.SetSize(120, height)
	m.drain()
	m.Update(key("a"))
	return m
}

func stamped(minutesAgo int, level string) string {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC).Add(-time.Duration(minutesAgo) * time.Minute)
	return fmt.Sprintf(`{"time":%q,"level":%q,"msg":"m"}`, at.Format(time.RFC3339), level)
}

// The panel says how many of each level there are now and in all, and names
// the window "now" means and the clock that placed the lines in it.
func TestStatsPanelCountsRecentAndAll(t *testing.T) {
	m := statsModel(t, 30,
		stamped(10, "info"), stamped(4, "error"), stamped(4, "info"), stamped(1, "info"))
	view := m.View()
	for _, text := range []string{"levels", "last 1m", "by log time"} {
		if !strings.Contains(view, text) {
			t.Errorf("%q missing from the panel:\n%s", text, view)
		}
	}
	// One of the three info lines is inside the minute; the error is four
	// minutes old and counts only in the total.
	for _, row := range []string{"info               1     3", "error              0     1"} {
		if !strings.Contains(view, row) {
			t.Errorf("row %q missing from the panel:\n%s", row, view)
		}
	}
}

// The levels are drawn as one bar of the panel's width, so how much of the
// log is a problem is readable without doing the sums — and the worst level
// is at the left edge, where it is always in the same place.
func TestStatsPanelDrawsTheLevelsAsOneBar(t *testing.T) {
	m := statsModel(t, 30,
		stamped(10, "info"), stamped(4, "error"), stamped(4, "info"), stamped(1, "info"))
	if bar := strings.Repeat("█", statsWidth-1); !strings.Contains(m.View(), bar) {
		t.Errorf("no bar %d cells wide in the panel:\n%s", statsWidth-1, m.View())
	}

	ordered := worstFirst([]logs.Count{{Key: "info", N: 3}, {Key: "error", N: 1}, {Key: "warn", N: 2}})
	for index, want := range []string{"error", "warn", "info"} {
		if ordered[index].Key != want {
			t.Errorf("levels ordered %+v, want the worst first", ordered)
			break
		}
	}
}

// Plain text has no timestamps, and the panel says it placed the lines by
// when they arrived rather than counting them as if it knew better.
func TestStatsPanelSaysWhenItPlacesByArrival(t *testing.T) {
	m := statsModel(t, 30, "starting", "listening on :8080")
	if view := m.View(); !strings.Contains(view, "by arrival") {
		t.Errorf("arrival clock not named:\n%s", view)
	}
}

// A filter narrows the lines the panel counts, and the panel says what it is
// counting out of. The levels list still holds the level the filter hides,
// because that list is where the next one is picked from.
func TestStatsPanelCountsTheFilteredView(t *testing.T) {
	m := statsModel(t, 30, stamped(3, "error"), stamped(2, "info"))
	m.Update(key("f"))
	typeText(m, "level=error")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if !strings.Contains(view, "1 of 2 lines") {
		t.Errorf("the panel does not say what it is counting out of:\n%s", view)
	}
	if !strings.Contains(view, "info ") {
		t.Errorf("the level the filter hides left the list it is picked from:\n%s", view)
	}
}

// However many counts there are, the panel is no taller than the log beside
// it, so the footer stays on the screen.
func TestStatsPanelFitsTheTerminal(t *testing.T) {
	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf(`{"level":"l%d","path":"/p%d"}`, i%12, i))
	}
	for _, height := range []int{12, 20, 40} {
		m := statsModel(t, height, lines...)
		view := m.View()
		if got := strings.Count(view, "\n") + 1; got > height {
			t.Errorf("at height %d the view is %d lines:\n%s", height, got, view)
		}
		for index, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > 120 {
				t.Errorf("line %d is %d columns wide", index, w)
			}
		}
		if !strings.Contains(view, "lines ·") {
			t.Errorf("at height %d the footer or the totals are gone:\n%s", height, view)
		}
	}
}

// A log follow opens with the backlog docker replays, which all arrives at
// once: placed by arrival it would count as the last minute's. The burst
// ends at the first drain that finds nothing, and only what comes after is
// recent.
func TestStatsPanelLeavesTheBacklogOutOfRecent(t *testing.T) {
	noon := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	feed, ch := feedOf(lineEvents(`{"level":"error"}`, `{"level":"error"}`, `{"level":"error"}`)...)
	m := New("deploy@prod", "logs: web", feed)
	m.now = func() time.Time { return noon }
	m.SetSize(120, 30)
	m.drain()
	m.drain() // nothing new: the replay is over

	ch <- operations.Event{Kind: operations.EventLog, Text: `{"level":"error"}`}
	m.drain()
	m.Update(key("a"))
	if row := "error              1     4"; !strings.Contains(m.View(), row) {
		t.Errorf("row %q missing: the backlog was counted as recent\n%s", row, m.View())
	}
}

// A feed that never paused is live after backlogMax whatever the drains say,
// and a command's output has no backlog at all.
func TestBacklogEndsWithoutAPause(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	feed, ch := feedOf(lineEvents(`{"level":"error"}`)...)
	m := New("deploy@prod", "logs: web", feed)
	m.now = func() time.Time { return now }
	m.drain()
	now = now.Add(backlogMax)
	ch <- operations.Event{Kind: operations.EventLog, Text: `{"level":"error"}`}
	m.drain()
	if m.backlogOpen {
		t.Error("the backlog is still open after backlogMax")
	}

	output, _ := feedOf(operations.Event{Kind: operations.EventStdout, Text: "done"})
	command := New("deploy@prod", "run: migrate", output)
	command.drain()
	if !command.backlogFrom.IsZero() {
		t.Error("a command's output opened a backlog")
	}
}

// The panel picks the field worth counting by itself, and `t` steps to the
// next one, wrapping round.
func TestStatsPanelPicksAndStepsTheField(t *testing.T) {
	var lines []string
	for i := range 12 {
		lines = append(lines, fmt.Sprintf(`{"level":"info","route":"/r%d","status":%d,"id":"%d"}`,
			i%3, 200+i%2, i))
	}
	m := statsModel(t, 30, lines...)
	for _, want := range []string{"top route", "top status", "top id", "top route"} {
		if view := m.View(); !strings.Contains(view, want) {
			t.Fatalf("%q missing:\n%s", want, view)
		}
		m.Update(key("t"))
	}
}

// Picking from the panel: the arrows move the cursor over levels and values
// alike, enter adds the row to the filter, and enter again takes it out.
// Two levels picked are either of them.
func TestStatsPanelPicksTheFilter(t *testing.T) {
	m := statsModel(t, 30,
		stamped(3, "error"), stamped(2, "warn"), stamped(2, "info"), stamped(1, "info"))
	if !m.statsFocus {
		t.Fatal("opening the panel did not hand it the keys")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // error, the top row
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // warn
	if got := m.store.Filter().Expr(); got != "level=error level=warn" {
		t.Fatalf("filter = %q, want the two levels picked", got)
	}
	if m.store.Len() != 2 {
		t.Errorf("%d lines in view, want the error and the warning", m.store.Len())
	}
	view := m.View()
	if !strings.Contains(view, "•error") || !strings.Contains(view, "2 of 4 lines") {
		t.Errorf("the picked rows are not marked:\n%s", view)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // warn again
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // error again
	if m.store.Filter() != nil {
		t.Errorf("picking both again left %q", m.store.Filter().Expr())
	}
}

// The cursor stays on the row it names while the counts reorder under it.
func TestStatsCursorFollowsItsRow(t *testing.T) {
	m := statsModel(t, 30, stamped(3, "info"), stamped(2, "debug"), stamped(2, "debug"))
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // info, below debug
	if m.picked.value != "info" {
		t.Fatalf("cursor on %q, want info", m.picked.value)
	}
	for range 3 {
		m.store.PushAt(stamped(1, "info"), m.now())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.store.Filter().Expr(); got != "level=info" {
		t.Errorf("filter = %q, want the row the cursor was left on", got)
	}
}

// tab and esc hand the keys between the panel and the log; esc from the log
// still goes back.
func TestStatsFocusMovesWithTabAndEsc(t *testing.T) {
	m := statsModel(t, 30, stamped(1, "info"))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.statsFocus || !m.showStats {
		t.Fatal("esc from the panel did not hand the keys to the log")
	}
	if !strings.Contains(m.View(), "tab panels") {
		t.Errorf("the footer does not say how to reach the panel:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !m.statsFocus {
		t.Fatal("tab did not hand the keys to the panel")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Error("esc from the log did not go back")
	}
}

// x resets the view at once — the whole filter and the search — from the
// panel or from the log, and is offered only while there is something to
// reset.
func TestResetClearsFilterAndSearch(t *testing.T) {
	m := statsModel(t, 30, stamped(3, "error"), stamped(2, "warn"), stamped(1, "info"))
	if strings.Contains(m.View(), "x reset") {
		t.Errorf("reset offered with nothing to reset:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "x reset") {
		t.Errorf("reset not offered with a filter set:\n%s", m.View())
	}
	m.Update(key("x"))
	if m.store.Filter() != nil || m.store.Len() != 3 {
		t.Errorf("x from the panel left %q with %d lines", m.store.Filter().Expr(), m.store.Len())
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // keys to the log
	m.Update(key("/"))
	typeText(m, "warn")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(key("x"))
	if m.store.Filter() != nil || m.query != "" || m.matchLine != -1 {
		t.Errorf("x from the log left filter %q and search %q", m.store.Filter().Expr(), m.query)
	}
}

// The footer is the dashboard's: the keys that work anywhere on the left, a
// rule, then the view's status and the focused region's keys.
func TestFooterSplitsGlobalFromFocused(t *testing.T) {
	m := statsModel(t, 30, stamped(1, "info"))
	footer := m.footer()
	left, right, found := strings.Cut(footer, "│")
	if !found {
		t.Fatalf("no rule in the footer: %q", footer)
	}
	for _, key := range []string{"tab panels", "/ search", "a stats", "q quit"} {
		if !strings.Contains(left, key) {
			t.Errorf("%q not on the left: %q", key, footer)
		}
	}
	for _, key := range []string{"1 lines", "esc log", "enter filter"} {
		if !strings.Contains(right, key) {
			t.Errorf("%q not on the right: %q", key, footer)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if _, right, _ := strings.Cut(m.footer(), "│"); !strings.Contains(right, "esc back") {
		t.Errorf("the log's keys are not on the right: %q", m.footer())
	}
}

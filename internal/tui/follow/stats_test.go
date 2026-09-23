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

// The counts are of the lines in view, so a filter narrows them and the
// panel says what it is counting out of — which is the answer to "how many
// of these are errors".
func TestStatsPanelCountsTheFilteredView(t *testing.T) {
	m := statsModel(t, 30, stamped(3, "error"), stamped(2, "info"))
	m.Update(key("f"))
	typeText(m, "level=error")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if !strings.Contains(view, "1 of 2 lines") {
		t.Errorf("the panel does not say what it is counting out of:\n%s", view)
	}
	if strings.Contains(view, "info ") {
		t.Errorf("a level the filter hides is still counted:\n%s", view)
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
		m.Update(key("t"))
		typeText(m, "path")
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

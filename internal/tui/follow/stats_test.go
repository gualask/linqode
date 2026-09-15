package follow

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// The panel says when, by which clock, and how the levels divide.
func TestStatsPanelDrawsTheTimelineAndShares(t *testing.T) {
	m := statsModel(t, 30,
		stamped(10, "info"), stamped(4, "error"), stamped(4, "info"), stamped(1, "info"))
	view := m.View()
	for _, text := range []string{"timeline", "by log time", "-15m", "now", "█"} {
		if !strings.Contains(view, text) {
			t.Errorf("%q missing from the panel:\n%s", text, view)
		}
	}
	// Three of four lines are info: its bar is three quarters of eight cells,
	// padded to eight so the names line up.
	if !strings.Contains(view, "     3 ██████   info") {
		t.Errorf("info's share not drawn as six cells:\n%s", view)
	}
}

// Plain text has no timestamps, and the timeline says it placed the lines by
// when they arrived rather than drawing them as if it knew better.
func TestStatsPanelSaysWhenItPlacesByArrival(t *testing.T) {
	m := statsModel(t, 30, "starting", "listening on :8080")
	if view := m.View(); !strings.Contains(view, "by arrival") {
		t.Errorf("arrival clock not named:\n%s", view)
	}
}

// A filter narrows the timeline, and the panel says so, because the counts
// under it are still of the whole tail.
func TestStatsPanelMarksAFilteredTimeline(t *testing.T) {
	m := statsModel(t, 30, stamped(3, "error"), stamped(2, "info"))
	m.Update(key("f"))
	typeText(m, "level=error")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if view := m.View(); !strings.Contains(view, "filtered") {
		t.Errorf("filtered timeline not marked:\n%s", view)
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

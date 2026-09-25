package follow

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/operations"
)

func numberedLines(n int) []operations.Event {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lineEvents(lines...)
}

// The cursor rides the newest line while following, moves without scrolling
// while it stays on screen, and scrolls the view only to stay on it.
func TestCursorMovesOverTheLogAndScrollsOnlyAtTheEdge(t *testing.T) {
	m := newTestModel(numberedLines(30)...) // viewport 10
	m.View()
	if m.selected != 29 || m.scroll != 20 {
		t.Fatalf("following: selected=%d scroll=%d, want 29 and 20", m.selected, m.scroll)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.follow || m.selected != 28 || m.scroll != 20 {
		t.Errorf("up: follow=%v selected=%d scroll=%d, want off, 28, 20", m.follow, m.selected, m.scroll)
	}
	for range 9 {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.selected != 19 || m.scroll != 19 {
		t.Errorf("past the top edge: selected=%d scroll=%d, want 19 and 19", m.selected, m.scroll)
	}

	for range 10 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if !m.follow || m.selected != 29 {
		t.Errorf("back on the newest line: follow=%v selected=%d", m.follow, m.selected)
	}
}

// Moved off the newest line, the cursor stays on the line it is on while new
// ones arrive: a line being read must not slide away.
func TestCursorStaysOnItsLineAsTheLogGrows(t *testing.T) {
	feed, ch := feedOf(numberedLines(30)...)
	m := New("deploy@prod", "logs: web", feed)
	m.SetSize(80, 12)
	m.drain()
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})

	for i := 30; i < 40; i++ {
		ch <- operations.Event{Kind: operations.EventLog, Text: fmt.Sprintf("line %d", i)}
	}
	m.drain()
	m.View()
	if line, _ := m.store.Line(m.selected); line.Raw != "line 27" {
		t.Errorf("cursor on %q, want line 27", line.Raw)
	}
}

// enter opens the line in full: a message the log cuts to the terminal is
// wrapped whole, every field gets its own row, its own line breaks survive,
// and esc goes back to the log where it was.
func TestEnterOpensTheWholeLine(t *testing.T) {
	long := strings.Repeat("word ", 40) + "END"
	line := fmt.Sprintf(`{"ts":"12:00:01","level":"error","msg":%q,"user":"alice","stack":"at a()\nat b()"}`, long)
	m := newTestModel(lineEvents(line, line, line)...)
	if strings.Contains(ansi.Strip(m.View()), "END") {
		t.Fatal("the log should cut the long message; the test needs it cut")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := ansi.Strip(m.View())
	for _, want := range []string{"12:00:01", "error", "END", "user   alice", "at a()\n", "at b()"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail misses %q:\n%s", want, view)
		}
	}
	for _, row := range strings.Split(view, "\n") {
		if ansi.StringWidth(row) > 80 {
			t.Errorf("row wider than the terminal: %q", row)
		}
	}

	// Back on the log, stopped where the line was opened; l resumes.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if view := m.View(); m.detail != nil || !strings.Contains(view, "l live") {
		t.Errorf("esc should return to the log, stopped:\n%s", view)
	}
}

// The search puts the cursor on its match, so / then enter then enter opens
// the line it found.
func TestSearchThenEnterOpensTheMatch(t *testing.T) {
	m := newTestModel(lineEvents("alpha", "beta target", "gamma", "delta")...)
	m.Update(key("/"))
	typeText(m, "target")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.detail == nil || m.detail.Raw != "beta target" {
		t.Fatalf("detail = %+v, want the matched line", m.detail)
	}
}

// The open line is held by value: the tail dropping its oldest lines under it
// does not swap what is being read.
func TestDetailSurvivesTheTailMoving(t *testing.T) {
	feed, ch := feedOf(numberedLines(5)...)
	m := New("deploy@prod", "logs: web", feed)
	m.SetSize(80, 12)
	m.drain()
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	for i := range logCapacity {
		ch <- operations.Event{Kind: operations.EventLog, Text: fmt.Sprintf("new %d", i)}
		if i%10 == 9 {
			m.drain()
		}
	}
	m.drain()
	if view := ansi.Strip(m.View()); !strings.Contains(view, "line 0") {
		t.Errorf("detail changed under the reader:\n%s", view)
	}
}

// A detail longer than the screen scrolls; q still quits from it.
func TestDetailScrollsAndQQuits(t *testing.T) {
	fields := make([]string, 30)
	for i := range fields {
		fields[i] = fmt.Sprintf(`"k%02d":"v%02d"`, i, i)
	}
	line := `{"msg":"many",` + strings.Join(fields, ",") + `}`
	m := newTestModel(lineEvents(line, line, line)...)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if view := m.View(); strings.Contains(view, "k29") || !strings.Contains(view, "↑↓ scroll") {
		t.Fatalf("first page should end before k29 and offer scrolling:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if view := m.View(); !strings.Contains(view, "k29") {
		t.Errorf("end should reach the last field:\n%s", view)
	}
	if cmd := m.Update(key("q")); cmd == nil {
		t.Fatal("q should quit from the detail")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q should quit from the detail")
	}
}

// Moving the cursor stops following; l picks it up again, and the footer
// offers it only while there is something to pick up.
func TestLResumesFollowing(t *testing.T) {
	m := newTestModel(numberedLines(30)...)
	if strings.Contains(m.View(), "l live") {
		t.Error("l live offered while already following")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if view := m.View(); m.follow || !strings.Contains(view, "l live") {
		t.Fatalf("after moving up: follow=%v, footer should offer l:\n%s", m.follow, view)
	}
	m.Update(key("l"))
	m.View()
	if !m.follow || m.selected != 29 {
		t.Errorf("l: follow=%v selected=%d, want following on line 29", m.follow, m.selected)
	}
}

// Opening a line while following stops following, so esc comes back to the
// line that was read rather than to a tail that has moved on.
func TestOpeningALineStopsFollowing(t *testing.T) {
	feed, events := feedOf(numberedLines(5)...)
	m := New("deploy@prod", "logs: web", feed)
	m.SetSize(80, 10)
	m.drain()
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	opened := m.detail.Raw
	for _, event := range numberedLines(20) {
		events <- event
	}
	m.drain()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.View()
	if m.follow {
		t.Fatal("still following after a line was opened")
	}
	if line, _ := m.store.Line(m.selected); line.Raw != opened {
		t.Errorf("back on %q, want the line that was opened, %q", line.Raw, opened)
	}
}

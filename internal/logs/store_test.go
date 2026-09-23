package logs

// Tests for the filtered visible view and the aggregations, ported from
// the Rust reference suite.

import (
	"slices"
	"testing"
	"time"
)

func storeOf(lines ...string) *Store {
	s := NewStore(100)
	for _, line := range lines {
		s.Push(line)
	}
	return s
}

func TestUnfilteredViewIsTheWholeTail(t *testing.T) {
	s := storeOf("a", "b")
	if s.Len() != 2 || s.Total() != 2 {
		t.Errorf("len %d total %d", s.Len(), s.Total())
	}
	if line, _ := s.Line(0); line.Raw != "a" {
		t.Errorf("line 0 %q", line.Raw)
	}
}

func TestFilterNarrowsViewAndRebuildsOnChange(t *testing.T) {
	s := storeOf(
		`{"level":"info","msg":"one"}`,
		"plain text",
		`{"level":"error","msg":"two"}`,
		`{"level":"error","msg":"three"}`,
	)
	s.SetFilter(filter(t, "level=error"))
	if s.Len() != 2 || s.Total() != 4 {
		t.Errorf("len %d total %d", s.Len(), s.Total())
	}
	line, _ := s.Line(0)
	if msg, _ := line.Record.Message(); msg != "two" {
		t.Errorf("first visible message %q", msg)
	}
	s.SetFilter(nil)
	if s.Len() != 4 {
		t.Errorf("len %d after clearing", s.Len())
	}
}

func TestFilteredViewTracksPushesAndDrops(t *testing.T) {
	s := NewStore(3)
	s.SetFilter(filter(t, "level=error"))
	if got := s.Push(`{"level":"error","n":1}`); got != 0 {
		t.Errorf("push 1 dropped %d", got)
	}
	if got := s.Push(`{"level":"info"}`); got != 0 {
		t.Errorf("push 2 dropped %d", got)
	}
	if got := s.Push(`{"level":"error","n":2}`); got != 0 {
		t.Errorf("push 3 dropped %d", got)
	}
	if s.Len() != 2 {
		t.Errorf("len %d", s.Len())
	}
	// Buffer is full: the next push drops the first error line.
	if got := s.Push(`{"level":"info"}`); got != 1 {
		t.Errorf("push 4 dropped %d", got)
	}
	if s.Len() != 1 {
		t.Errorf("len %d", s.Len())
	}
	line, _ := s.Line(0)
	if n, _ := line.Record.Get("n"); n != "2" {
		t.Errorf("surviving error n=%q", n)
	}
	// Dropping a non-matching line does not disturb the view.
	if got := s.Push(`{"level":"info"}`); got != 0 {
		t.Errorf("push 5 dropped %d", got)
	}
	if s.Len() != 1 {
		t.Errorf("len %d", s.Len())
	}
}

func TestSearchRunsOverTheVisibleView(t *testing.T) {
	s := storeOf(
		`{"level":"error","msg":"alpha"}`,
		`{"level":"info","msg":"alpha"}`,
		`{"level":"error","msg":"beta"}`,
	)
	if i, ok := s.SearchNext("alpha", 0); !ok || i != 0 {
		t.Errorf("unfiltered: %d ok=%v", i, ok)
	}
	s.SetFilter(filter(t, "level=error"))
	// Visible view is [alpha(error), beta(error)]: index 1 is beta.
	if i, ok := s.SearchNext("beta", 0); !ok || i != 1 {
		t.Errorf("beta: %d ok=%v", i, ok)
	}
	if i, ok := s.SearchNext("alpha", 1); !ok || i != 0 { // wrapped
		t.Errorf("alpha wrapped: %d ok=%v", i, ok)
	}
	if i, ok := s.SearchPrev("beta", 0); !ok || i != 1 { // wrapped
		t.Errorf("beta wrapped back: %d ok=%v", i, ok)
	}
	if _, ok := s.SearchNext("info", 0); ok {
		t.Error("info is filtered out, must not be found")
	}
}

func TestDetectsMostlyJSONLStreams(t *testing.T) {
	s := NewStore(100)
	if s.LooksStructured() {
		t.Error("empty store looks structured")
	}
	s.Push(`{"level":"info"}`)
	s.Push(`{"level":"info"}`)
	if s.LooksStructured() {
		t.Error("below the floor")
	}
	s.Push(`{"level":"info"}`)
	if !s.LooksStructured() {
		t.Error("3 parsed lines should detect")
	}
	for range 3 {
		s.Push("plain")
	}
	if s.LooksStructured() {
		t.Error("no longer a majority")
	}
}

func TestStatsCountLevelsCaseFoldedAndSorted(t *testing.T) {
	s := storeOf(
		`{"level":"ERROR"}`,
		`{"level":"error"}`,
		`{"level":"info"}`,
		"plain-text line",
	)
	stats := s.ComputeStats("", time.Now())
	if stats.Lines != 4 || stats.Parsed != 3 {
		t.Errorf("lines %d parsed %d", stats.Lines, stats.Parsed)
	}
	// Everything was pushed a moment ago, so every count is recent.
	want := []Count{{"error", 2, 2}, {"info", 1, 1}}
	if !slices.Equal(stats.Levels, want) {
		t.Errorf("levels %v", stats.Levels)
	}
}

func TestStatsCountTopValuesOfChosenField(t *testing.T) {
	s := NewStore(100)
	for _, path := range []string{"/a", "/b", "/a", "/c", "/a", "/b"} {
		s.Push(`{"path":"` + path + `"}`)
	}
	stats := s.ComputeStats("path", time.Now())
	want := []Count{{"/a", 3, 3}, {"/b", 2, 2}, {"/c", 1, 1}}
	if !slices.Equal(stats.Values, want) {
		t.Errorf("values %v", stats.Values)
	}
	if len(s.ComputeStats("", time.Now()).Values) != 0 {
		t.Error("no field chosen, values must be empty")
	}
}

func TestStatsTiesBreakAlphabetically(t *testing.T) {
	s := storeOf(`{"k":"b"}`, `{"k":"a"}`)
	stats := s.ComputeStats("k", time.Now())
	want := []Count{{"a", 1, 1}, {"b", 1, 1}}
	if !slices.Equal(stats.Values, want) {
		t.Errorf("values %v", stats.Values)
	}
}

func TestStatsFollowBufferDrops(t *testing.T) {
	// The recompute-on-demand design must reflect dropped lines exactly:
	// push past capacity and check the counts match the surviving tail.
	s := NewStore(2)
	s.Push(`{"level":"error"}`)
	s.Push(`{"level":"info"}`)
	s.Push(`{"level":"info"}`) // drops the error line
	stats := s.ComputeStats("", time.Now())
	want := []Count{{"info", 2, 2}}
	if !slices.Equal(stats.Levels, want) {
		t.Errorf("levels %v", stats.Levels)
	}
	if stats.Lines != 2 || stats.Parsed != 2 {
		t.Errorf("lines %d parsed %d", stats.Lines, stats.Parsed)
	}
}

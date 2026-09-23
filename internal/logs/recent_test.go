package logs

import (
	"testing"
	"time"
)

// Lines placed by their own timestamps, the traceback under a record placed
// with it, and the counts split into what is recent and what is everything.
func TestRecentCountsPlaceByLogTime(t *testing.T) {
	s := NewStore(100)
	arrived := noon // the whole backlog arrived at once
	stamp := func(ago time.Duration, level string) string {
		return `{"time":"` + noon.Add(-ago).Format(time.RFC3339) + `","level":"` + level + `"}`
	}
	s.PushAt(stamp(40*time.Minute, "info"), arrived)
	s.PushAt(stamp(30*time.Second, "error"), arrived)
	s.PushAt("Traceback (most recent call last):", arrived)
	s.PushAt(stamp(20*time.Second, "error"), arrived)

	stats := s.ComputeStats("", noon)
	if stats.Clock != ByLogTime {
		t.Fatalf("clock = %v, want log time", stats.Clock)
	}
	// Three of the four lines are inside the minute, which is more than the
	// tenth the shortest window needs.
	if stats.Recent != time.Minute {
		t.Errorf("window = %v, want 1m", stats.Recent)
	}
	want := []Count{{"error", 2, 2}, {"info", 1, 0}}
	for index, got := range stats.Levels {
		if index >= len(want) || got != want[index] {
			t.Errorf("levels = %+v, want %+v", stats.Levels, want)
			break
		}
	}
	// The plain line under the error carries no level and is counted in
	// neither list, but it is one of the lines in view.
	if stats.Lines != 4 || stats.Parsed != 3 {
		t.Errorf("lines %d parsed %d, want 4 and 3", stats.Lines, stats.Parsed)
	}
}

// A stream whose lines mostly carry no timestamp is placed by arrival, all
// of it, rather than half by one clock and half by the other.
func TestRecentFallsBackToArrival(t *testing.T) {
	s := NewStore(100)
	s.PushAt(`{"time":"2020-01-01T00:00:00Z","level":"error"}`, noon.Add(-50*time.Second))
	s.PushAt("plain", noon.Add(-40*time.Second))
	s.PushAt("plain", noon.Add(-5*time.Second))

	stats := s.ComputeStats("", noon)
	if stats.Clock != ByArrival {
		t.Fatalf("clock = %v, want arrival", stats.Clock)
	}
	// Placed by arrival the error is fifty seconds old, not six years.
	if stats.Recent != time.Minute {
		t.Errorf("window = %v, want 1m", stats.Recent)
	}
	if got := stats.Levels[0]; got.N != 1 || got.Recent != 1 {
		t.Errorf("error count = %+v, want one of it, recent", got)
	}
}

// The window follows the pace of what is in view: a service that logs twice
// a second is counted over a minute, one that logs twice an hour over a day,
// so the column is never a row of noughts and never the whole tail again.
func TestRecentWindowFollowsThePace(t *testing.T) {
	busy := NewStore(100)
	for i := range 20 {
		busy.PushAt(`{"level":"info"}`, noon.Add(-time.Duration(i)*time.Second))
	}
	if window := busy.ComputeStats("", noon).Recent; window != time.Minute {
		t.Errorf("a line a second gave a window of %v, want 1m", window)
	}

	slow := NewStore(100)
	for i := range 20 {
		slow.PushAt(`{"level":"info"}`, noon.Add(-time.Duration(i)*30*time.Minute))
	}
	// Three of the twenty are inside the hour — now, half an hour ago, and
	// the one exactly on the edge, which counts as inside it — and two is
	// the tenth the window takes.
	stats := slow.ComputeStats("", noon)
	if stats.Recent != time.Hour {
		t.Errorf("a line every half hour gave a window of %v, want 1h", stats.Recent)
	}
	if got := stats.Levels[0]; got.N != 20 || got.Recent != 3 {
		t.Errorf("info count = %+v, want 20 with 3 recent", got)
	}
}

// The counts are of the lines in view, so a filter narrows them — which is
// the whole of "how many of these are errors".
func TestCountsFollowTheFilter(t *testing.T) {
	s := NewStore(100)
	for _, level := range []string{"info", "error", "info", "error", "info"} {
		s.PushAt(`{"level":"`+level+`","route":"/a"}`, noon)
	}
	filter, err := ParseFilter("level=error")
	if err != nil {
		t.Fatal(err)
	}
	s.SetFilter(filter)

	stats := s.ComputeStats("route", noon)
	if stats.Lines != 2 || stats.Tail != 5 {
		t.Errorf("lines %d of a tail of %d, want 2 of 5", stats.Lines, stats.Tail)
	}
	if len(stats.Levels) != 1 || stats.Levels[0].N != 2 {
		t.Errorf("levels = %+v, want the two errors alone", stats.Levels)
	}
	if len(stats.Values) != 1 || stats.Values[0].N != 2 {
		t.Errorf("values = %+v, want the route counted over the filtered view", stats.Values)
	}
}

// Nothing in view is a window and no counts, not a crash.
func TestRecentOnAnEmptyStore(t *testing.T) {
	stats := NewStore(10).ComputeStats("", noon)
	if stats.Lines != 0 || len(stats.Levels) != 0 || stats.Recent == 0 {
		t.Errorf("an empty store gave %+v", stats)
	}
}

// The backlog a follow opens with arrived in one burst, and placed by
// arrival it would all be "in the last minute". It is left out of the recent
// counts instead — still in the totals — and what arrives after it is
// counted as it comes, filtered or not.
func TestBacklogIsNotRecentByArrival(t *testing.T) {
	s := NewStore(100)
	s.OpenBacklog()
	for range 50 {
		s.PushAt(`{"level":"error"}`, noon.Add(-time.Second))
	}
	s.CloseBacklog()
	s.PushAt(`{"level":"error"}`, noon)
	s.PushAt(`{"level":"info"}`, noon)

	check := func(what string) {
		t.Helper()
		stats := s.ComputeStats("", noon)
		if stats.Clock != ByArrival || stats.Recent != time.Minute {
			t.Errorf("%s: clock %v window %v, want arrival over 1m", what, stats.Clock, stats.Recent)
		}
		if got := stats.Levels[0]; got.Key != "error" || got.N != 51 || got.Recent != 1 {
			t.Errorf("%s: errors = %+v, want 51 with the live one recent", what, got)
		}
	}
	check("unfiltered")
	filter, err := ParseFilter("level=error")
	if err != nil {
		t.Fatal(err)
	}
	s.SetFilter(filter)
	check("filtered")
}

// A backlog still arriving is backlog to its last line: nothing pushed
// before CloseBacklog is recent.
func TestOpenBacklogHoldsEverythingPushed(t *testing.T) {
	s := NewStore(100)
	s.OpenBacklog()
	s.PushAt(`{"level":"error"}`, noon)
	if got := s.ComputeStats("", noon).Levels[0]; got.Recent != 0 {
		t.Errorf("a line of an open backlog counted as recent: %+v", got)
	}
}

// The window does not jump every frame. It is kept until it holds under
// half the share that chose it, where a window chosen afresh would already
// have moved on.
func TestRecentWindowHoldsUntilItEmpties(t *testing.T) {
	fill := func() *Store {
		s := NewStore(100)
		for range 18 {
			s.PushAt(`{"level":"info"}`, noon.Add(-3*time.Minute))
		}
		s.PushAt(`{"level":"info"}`, noon.Add(-40*time.Second))
		s.PushAt(`{"level":"info"}`, noon)
		return s
	}
	s := fill()
	if window := s.ComputeStats("", noon).Recent; window != time.Minute {
		t.Fatalf("two of twenty inside the minute gave %v, want 1m", window)
	}

	// Thirty seconds on, one line is inside the minute: under the tenth that
	// chose it, not under half of that.
	later := noon.Add(30 * time.Second)
	if window := fill().ComputeStats("", later).Recent; window != 5*time.Minute {
		t.Fatalf("a fresh store gave %v, want 5m", window)
	}
	if window := s.ComputeStats("", later).Recent; window != time.Minute {
		t.Errorf("the window moved to %v with a line still inside it, want 1m kept", window)
	}

	// Two minutes on the minute is empty, and the window lengthens.
	if window := s.ComputeStats("", noon.Add(2*time.Minute)).Recent; window != 5*time.Minute {
		t.Errorf("an empty minute was kept as the window: %v, want 5m", window)
	}
}

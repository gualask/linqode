package logs

import (
	"testing"
	"time"
)

var noon = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

// A written timestamp is read only when it says which zone it is in, and a
// number only when it lands somewhere a log could have been written.
func TestRecordTimeReadsZonedAndEpochTimestamps(t *testing.T) {
	cases := []struct {
		line string
		want time.Time
	}{
		{`{"time":"2026-09-15T12:00:00Z"}`, noon},
		{`{"time":"2026-09-15T14:00:00.250+02:00"}`, noon.Add(250 * time.Millisecond)},
		{`{"ts":"2026-09-15 12:00:00Z"}`, noon},
		{`{"@timestamp":"2026-09-15T12:00:00+0000"}`, noon},
		{`{"time":"2026-09-15 12:00:00 +0000 UTC"}`, noon},
		{`{"ts":1789473600.5}`, noon.Add(500 * time.Millisecond)},
		{`{"time":1789473600000}`, noon},
		{`{"time":1789473600000000}`, noon},
		{`{"time":1789473600000000000}`, noon},
		{`{"ts":"1789473600"}`, noon},
	}
	for _, c := range cases {
		got, ok := ParseRecord(c.line).Time()
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s read as %v (%v), want %v", c.line, got, ok, c.want)
		}
	}
	for _, line := range []string{
		`{"time":"2026-09-15 12:00:00"}`, // no zone: a guess would move it by hours
		`{"time":"12:00:01"}`,
		`{"ts":12.5}`, // seconds since the process started
		`{"time":"yesterday"}`,
		`{"msg":"no time at all"}`,
	} {
		if got, ok := ParseRecord(line).Time(); ok {
			t.Errorf("%s read as %v, want no time", line, got)
		}
	}
}

// Lines placed by their own timestamps, the traceback under a record placed
// with it, and the span the shortest that reaches back to the oldest line.
func TestTimelinePlacesByLogTime(t *testing.T) {
	s := NewStore(100)
	arrived := noon // the whole backlog arrived at once
	stamp := func(ago time.Duration, level string) string {
		return `{"time":"` + noon.Add(-ago).Format(time.RFC3339) + `","level":"` + level + `"}`
	}
	s.PushAt(stamp(10*time.Minute, "info"), arrived)
	s.PushAt(stamp(4*time.Minute, "error"), arrived)
	s.PushAt("Traceback (most recent call last):", arrived)
	s.PushAt(stamp(4*time.Minute, "warn"), arrived)
	s.PushAt(stamp(30*time.Second, "info"), arrived)

	timeline := s.Timeline(noon, 15)
	if timeline.Clock != ByLogTime {
		t.Fatalf("clock = %v, want log time", timeline.Clock)
	}
	if timeline.Span != 15*time.Minute {
		t.Errorf("span = %v, want the 15m that reaches back ten minutes", timeline.Span)
	}
	if timeline.Placed != 5 || timeline.Older != 0 {
		t.Errorf("placed %d, older %d, want 5 and 0", timeline.Placed, timeline.Older)
	}
	// Fifteen buckets over fifteen minutes, one a minute, starting at 11:45:
	// ten minutes ago is the sixth, four minutes ago the twelfth.
	want := map[int]Bucket{
		5:  {Lines: 1, Worst: SeverityNone},
		11: {Lines: 3, Worst: SeverityError},
		14: {Lines: 1, Worst: SeverityNone},
	}
	for index, bucket := range timeline.Buckets {
		if bucket != want[index] {
			t.Errorf("bucket %d = %+v, want %+v", index, bucket, want[index])
		}
	}
}

// A stream whose lines mostly carry no timestamp is placed by arrival, all of
// it, rather than half by one clock and half by the other.
func TestTimelineFallsBackToArrival(t *testing.T) {
	s := NewStore(100)
	s.PushAt(`{"time":"2020-01-01T00:00:00Z","level":"error"}`, noon.Add(-50*time.Second))
	s.PushAt("plain", noon.Add(-40*time.Second))
	s.PushAt("plain", noon.Add(-5*time.Second))
	timeline := s.Timeline(noon, 6)
	if timeline.Clock != ByArrival {
		t.Fatalf("clock = %v, want arrival", timeline.Clock)
	}
	if timeline.Span != time.Minute || timeline.Placed != 3 {
		t.Errorf("span %v placed %d, want 1m and 3", timeline.Span, timeline.Placed)
	}
	if timeline.Buckets[1].Worst != SeverityError || timeline.Buckets[5].Lines != 1 {
		t.Errorf("buckets = %+v", timeline.Buckets)
	}
}

// The timeline is of the lines in view, so a filter narrows it.
func TestTimelineFollowsTheFilter(t *testing.T) {
	s := NewStore(100)
	for _, level := range []string{"info", "error", "info", "error", "info"} {
		s.PushAt(`{"level":"`+level+`"}`, noon)
	}
	filter, err := ParseFilter("level=error")
	if err != nil {
		t.Fatal(err)
	}
	s.SetFilter(filter)
	if placed := s.Timeline(noon, 10).Placed; placed != 2 {
		t.Errorf("placed %d lines under a filter matching 2", placed)
	}
}

// A clock a little ahead lands in the last bucket; a line from before the
// longest span is counted rather than stretched over.
func TestTimelineClampsTheFutureAndCountsTheAncient(t *testing.T) {
	s := NewStore(100)
	s.PushAt(`{"time":"`+noon.Add(3*time.Second).Format(time.RFC3339)+`"}`, noon)
	s.PushAt(`{"time":"`+noon.Add(-8*24*time.Hour).Format(time.RFC3339)+`"}`, noon)
	timeline := s.Timeline(noon, 7)
	if timeline.Span != 7*24*time.Hour {
		t.Errorf("span = %v, want the longest", timeline.Span)
	}
	if timeline.Buckets[6].Lines != 1 || timeline.Older != 1 {
		t.Errorf("last bucket %+v, older %d", timeline.Buckets[6], timeline.Older)
	}
	if empty := NewStore(10).Timeline(noon, 5); empty.Placed != 0 || len(empty.Buckets) != 5 {
		t.Errorf("an empty store gave %+v", empty)
	}
}

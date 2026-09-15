package logs

// When the lines in view were written, in equal slices of time ending now.
//
// The counts beside it say how many errors the tail holds; this says when
// they happened, which is a different sentence about the same log — "thirty
// errors" and "thirty errors in the last two minutes" are two different
// problems. Like the counts, it is recomputed over the tail on demand rather
// than maintained: the work at render time is a pass of integer arithmetic,
// because every line's time was read once, when it arrived.

import (
	"strings"
	"time"
)

// Severity is how bad a level says a line is, reduced to the distinctions a
// timeline is coloured by.
type Severity int

const (
	SeverityNone Severity = iota
	SeverityWarning
	SeverityError
)

// SeverityOf reads a level. The names are the ones the log view colours, and
// that view asks this function rather than keeping a list of its own, so a
// level the timeline paints red is the level the lines beside it paint red.
func SeverityOf(level string) Severity {
	switch strings.ToLower(level) {
	case "error", "fatal", "critical", "panic":
		return SeverityError
	case "warn", "warning":
		return SeverityWarning
	default:
		return SeverityNone
	}
}

// Clock is which of a line's two times placed it.
type Clock int

const (
	// ByArrival places lines by when they reached this client. Every line
	// has that time, and it is wrong for the backlog the follow starts
	// with: those lines were written over hours and arrived in one burst.
	ByArrival Clock = iota
	// ByLogTime places lines by the timestamp their records carry.
	ByLogTime
)

// Bucket is one slice of the timeline.
type Bucket struct {
	Lines int
	// Worst is the most severe level among them.
	Worst Severity
}

// Timeline is the lines in view placed in time.
type Timeline struct {
	// Buckets are oldest first; the last one ends now.
	Buckets []Bucket
	Span    time.Duration
	Clock   Clock
	// Placed is how many lines landed in a bucket.
	Placed int
	// Older is how many lines were written before the longest span began.
	Older int
}

// timelineSpans are the stretches of time a timeline can cover. The shortest
// one that reaches back to the oldest line in view is chosen, so a quiet
// service's week and a busy one's two minutes both fill the width they are
// given.
var timelineSpans = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour,
	6 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour,
}

// Timeline places the lines in view — the filtered ones when a filter is
// set, since "when did these start" is the question a filter is there to
// ask — into buckets equal slices of time ending at now.
//
// The whole view is placed by one clock, never a mix of the two: a record
// written an hour ago and a line that arrived a second ago on the same axis
// would say the second came long after the first. Log time is used when at
// least half the lines carry a readable timestamp, and a line without one —
// the traceback under an error record — is placed at the time of the record
// before it, which is when it was written. Otherwise every line is placed by
// when it arrived, and the timeline says so.
//
// A line stamped after now, which is what a server clock a little ahead of
// this one produces, lands in the last bucket rather than nowhere.
func (s *Store) Timeline(now time.Time, buckets int) Timeline {
	var timeline Timeline
	if buckets <= 0 {
		return timeline
	}
	type entry struct {
		arrived, logged time.Time
		severity        Severity
	}
	entries := make([]entry, 0, s.Len())
	stamped := 0
	var carried time.Time
	for index := range s.Len() {
		line, _ := s.Line(index)
		if !line.Logged.IsZero() {
			carried = line.Logged
			stamped++
		}
		severity := SeverityNone
		if line.Record != nil {
			if level, ok := line.Record.Level(); ok {
				severity = SeverityOf(level)
			}
		}
		entries = append(entries, entry{arrived: line.Arrived, logged: carried, severity: severity})
	}
	if stamped > 0 && stamped*2 >= len(entries) {
		timeline.Clock = ByLogTime
	}

	oldest := now
	for _, entry := range entries {
		at := entry.arrived
		if timeline.Clock == ByLogTime {
			at = entry.logged
		}
		if !at.IsZero() && at.Before(oldest) {
			oldest = at
		}
	}
	timeline.Span = timelineSpans[len(timelineSpans)-1]
	for _, span := range timelineSpans {
		if now.Sub(oldest) <= span {
			timeline.Span = span
			break
		}
	}

	timeline.Buckets = make([]Bucket, buckets)
	start := now.Add(-timeline.Span)
	width := timeline.Span / time.Duration(buckets)
	for _, entry := range entries {
		at := entry.arrived
		if timeline.Clock == ByLogTime {
			at = entry.logged
		}
		switch {
		case at.IsZero():
			// Lines before the first stamped record have nothing to be
			// placed by.
			continue
		case at.Before(start):
			timeline.Older++
			continue
		}
		bucket := &timeline.Buckets[min(int(at.Sub(start)/width), buckets-1)]
		bucket.Lines++
		bucket.Worst = max(bucket.Worst, entry.severity)
		timeline.Placed++
	}
	return timeline
}

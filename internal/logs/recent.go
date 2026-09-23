package logs

// How much of what is in view is recent, and by which clock.
//
// The counts beside the log say how many errors it holds; on their own they
// cannot say whether those errors are still happening, which is the
// difference between an incident and a scar. A window ending now answers
// that in a number, on the row that already carries the level.
//
// This replaced a histogram of when the lines were written (removed
// September 2026). It drew the shape of the traffic over the same window —
// height for how many lines, colour for the worst level among them — which
// is a true thing to draw and a different question from the one the panel is
// read for. Reading it took knowing that the height counted every level
// while the colour spoke for one of them, and that neither was a number.
// What survived is the part the counts needed anyway: placing the lines in
// time, under one clock.

import "time"

// Clock is which of a line's two times placed it.
type Clock int

const (
	// ByArrival places lines by when they reached this client. Every line
	// has that time, and it is wrong for the backlog a follow starts with:
	// those lines were written over hours and arrived in one burst.
	ByArrival Clock = iota
	// ByLogTime places lines by the timestamp their records carry.
	ByLogTime
)

// recentWindows are the stretches the recent counts can cover. A fixed
// window would be a column of noughts on a service that logs twice an hour
// and the whole tail on one that logs twice a second, so the window follows
// the pace of what is in view — and the panel prints which one it landed on,
// because a number nobody can name the window of is not a reading.
var recentWindows = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour,
	6 * time.Hour, 24 * time.Hour,
}

// recentShare is the fraction of what is in view a window has to hold to be
// the one counted over: the shortest window holding a tenth of the lines.
// It is a tenth rather than a half because the column is there to say what
// is happening *now* — a window holding most of the tail says what the total
// beside it already said.
const recentShare = 10

// recency places the lines in view in time and picks the window the recent
// counts cover. It returns one time per visible line, in view order, zero
// where a line has nothing to be placed by.
//
// The whole view is placed by one clock, never a mix of the two: a record
// written an hour ago and a line that arrived a second ago on the same
// footing would say the second came long after the first. Log time is used
// when at least half the lines carry a readable timestamp, and a line
// without one — the traceback under an error record — is placed at the time
// of the record before it, which is when it was written.
func (s *Store) recency(now time.Time) (Clock, time.Duration, []time.Time) {
	arrived := make([]time.Time, s.Len())
	logged := make([]time.Time, s.Len())
	stamped := 0
	var carried time.Time
	for index := range s.Len() {
		line, _ := s.Line(index)
		if !line.Logged.IsZero() {
			carried = line.Logged
			stamped++
		}
		arrived[index], logged[index] = line.Arrived, carried
	}

	clock, at := ByArrival, arrived
	if stamped > 0 && stamped*2 >= len(arrived) {
		clock, at = ByLogTime, logged
	}

	// A line stamped after now, which is what a server clock a little ahead
	// of this one produces, is recent under every window rather than under
	// none.
	window := recentWindows[len(recentWindows)-1]
	for _, candidate := range recentWindows {
		if countSince(at, now.Add(-candidate))*recentShare >= len(at) {
			window = candidate
			break
		}
	}
	return clock, window, at
}

// countSince is how many of the placed lines are at or after cutoff.
func countSince(at []time.Time, cutoff time.Time) int {
	n := 0
	for _, moment := range at {
		if !moment.IsZero() && !moment.Before(cutoff) {
			n++
		}
	}
	return n
}

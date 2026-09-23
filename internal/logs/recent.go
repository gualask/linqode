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
	// those lines were written over hours and arrived in one burst, so they
	// are not placed at all (see Store.OpenBacklog). Counted as arriving
	// when they did, a follow would open on its whole tail under "last 1m"
	// — an incident in progress, on a service that has been quiet all day.
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
// the one counted over: the shortest window holding a tenth of the lines
// that could be placed at all.
// It is a tenth rather than a half because the column is there to say what
// is happening *now* — a window holding most of the tail says what the total
// beside it already said.
const recentShare = 10

// recency places every line of the tail in time and picks the window the
// recent counts cover. It returns one time per buffered line, oldest first,
// zero where a line has nothing to be placed by. The clock and the window
// are chosen over the lines in view — the ones the filter lets through —
// but every line is placed, because the stats panel counts a field over
// lines its own filter terms would hide.
//
// The whole view is placed by one clock, never a mix of the two: a record
// written an hour ago and a line that arrived a second ago on the same
// footing would say the second came long after the first. Log time is used
// when at least half the lines in view carry a readable timestamp, and a
// line without one — the traceback under an error record — is placed at the
// time of the record before it, which is when it was written.
//
// It remembers the window it chose, because it is asked on every frame and
// a window chosen afresh each time jumps: a service logging in bursts holds
// a tenth of its lines in the last minute, then just under, then just over,
// and every number in the column changes meaning with it. So a window is
// kept until it holds less than half the share that chose it; a shorter one
// is still taken as soon as it qualifies, and then kept the same way.
func (s *Store) recency(now time.Time) (Clock, time.Duration, []time.Time) {
	total := s.buffer.Len()
	arrived := make([]time.Time, total)
	logged := make([]time.Time, total)
	inView := make([]bool, total)
	stamped, viewed := 0, 0
	var carried time.Time
	for index := range total {
		line, _ := s.buffer.Get(index)
		if !line.Logged.IsZero() {
			carried = line.Logged
		}
		if s.filter == nil || s.filter.Matches(line.Record) {
			inView[index] = true
			viewed++
			if !line.Logged.IsZero() {
				stamped++
			}
		}
		if s.baseSeq+uint64(index) >= s.backlogEnd {
			arrived[index] = line.Arrived
		}
		logged[index] = carried
	}

	clock, at := ByArrival, arrived
	if stamped > 0 && stamped*2 >= viewed {
		clock, at = ByLogTime, logged
	}
	since := func(cutoff time.Time) int {
		n := 0
		for index, moment := range at {
			if inView[index] && !moment.IsZero() && !moment.Before(cutoff) {
				n++
			}
		}
		return n
	}
	placed := since(time.Time{})

	// A line stamped after now, which is what a server clock a little ahead
	// of this one produces, is recent under every window rather than under
	// none.
	window := recentWindows[len(recentWindows)-1]
	for _, candidate := range recentWindows {
		if since(now.Add(-candidate))*recentShare >= placed {
			window = candidate
			break
		}
	}
	if previous := s.window; previous != 0 && s.windowClock == clock && window > previous &&
		since(now.Add(-previous))*recentShare*2 >= placed {
		window = previous
	}
	s.window, s.windowClock = window, clock
	return clock, window, at
}

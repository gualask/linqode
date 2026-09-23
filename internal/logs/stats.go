package logs

import (
	"slices"
	"strings"
	"time"
)

// Severity is how bad a level says a line is, reduced to the distinctions
// the log view colours by.
type Severity int

const (
	SeverityNone Severity = iota
	SeverityWarning
	SeverityError
)

// SeverityOf reads a level. The names are the ones the log view colours, and
// that view asks this function rather than keeping a list of its own.
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

// Count is one aggregation bucket: how many in all, and how many of those
// are recent.
type Count struct {
	Key string
	N   int
	// Recent is how many of N fell inside Stats.Recent.
	Recent int
}

// Stats are aggregations over the lines currently in view: counts by
// (lowercased) level, and value counts for one chosen field, each split into
// what is recent and what is everything.
//
// They are of the lines *in view*, which means the filtered ones while a
// filter is set. "How many of these are errors" is the question a filter
// leaves you holding, and it used to be unanswerable here: the counts were
// of the whole tail whatever the filter said, so filtering a log down to one
// route still reported every level in the buffer.
//
// They are recomputed on demand rather than maintained incrementally: at the
// tail's bounded size (10k lines) a recompute is well under a millisecond,
// which beats carrying add/remove symmetry invariants through every buffer
// mutation.
type Stats struct {
	// Lines and Parsed are of the view; Tail is every line held, so what a
	// filter is hiding stays readable beside what it is showing.
	Lines  int
	Parsed int
	Tail   int
	Levels []Count
	// Field is the field Values counts; empty means none chosen.
	Field  string
	Values []Count
	// Recent is the window the Recent counts cover, and Clock is what placed
	// the lines in it.
	Recent time.Duration
	Clock  Clock
}

// ComputeStats aggregates the lines in view. field chooses the Values
// counts; empty leaves them out. now is what "recent" is measured back from.
func (s *Store) ComputeStats(field string, now time.Time) Stats {
	stats := Stats{Field: field, Tail: s.buffer.Len()}
	clock, window, at := s.recency(now)
	stats.Clock, stats.Recent = clock, window
	cutoff := now.Add(-window)

	levels := make(map[string]Count)
	values := make(map[string]Count)
	for index := range s.Len() {
		line, _ := s.Line(index)
		stats.Lines++
		if line.Record == nil {
			continue
		}
		stats.Parsed++
		recent := !at[index].IsZero() && !at[index].Before(cutoff)
		if level, ok := line.Record.Level(); ok {
			count(levels, strings.ToLower(level), recent)
		}
		if field != "" {
			if value, ok := line.Record.Get(field); ok {
				count(values, value, recent)
			}
		}
	}
	stats.Levels = sortedCounts(levels)
	stats.Values = sortedCounts(values)
	return stats
}

// count adds one line to a key, and to its recent share when it is one.
func count(counts map[string]Count, key string, recent bool) {
	bucket := counts[key]
	bucket.N++
	if recent {
		bucket.Recent++
	}
	counts[key] = bucket
}

// sortedCounts orders buckets highest first, ties breaking alphabetically.
// By the total rather than by what is recent: a list that reordered itself
// as the traffic moved would be unreadable at a glance, which is the only
// way it is read.
func sortedCounts(m map[string]Count) []Count {
	counts := make([]Count, 0, len(m))
	for key, bucket := range m {
		bucket.Key = key
		counts = append(counts, bucket)
	}
	slices.SortFunc(counts, func(a, b Count) int {
		if a.N != b.N {
			return b.N - a.N
		}
		return strings.Compare(a.Key, b.Key)
	})
	return counts
}

package logs

import (
	"math"
	"slices"
	"strconv"
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
//
// Loggers do not agree on a spelling. Beside the full words there are the
// three-letter forms of Serilog and zerolog's console writer (`ERR`, `WRN`,
// `FTL`), zap's `dpanic`, syslog's `emerg`, `alert` and `crit`, and
// java.util.logging's `SEVERE`. And pino and bunyan write the level as a
// number — 60 fatal, 50 error, 40 warn, 30 info, 20 debug, 10 trace — as a
// JSON number or, through some shippers, as a string of one. A number below
// ten is on no scale those two use and is left alone: syslog's 0 to 7 run
// the other way, and guessing would paint info as an error. Nor is anything
// ParseFloat calls infinite: that is how it reads Serilog's `INF`, for info.
func SeverityOf(level string) Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "err", "eror", "fatal", "ftl", "critical", "crit", "panic", "dpanic",
		"emerg", "emergency", "alert", "severe":
		return SeverityError
	case "warn", "warning", "wrn":
		return SeverityWarning
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(level), 64)
	switch {
	case err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 10:
		return SeverityNone
	case number >= 50:
		return SeverityError
	case number >= 40:
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

// Stats are aggregations over the tail: counts by (lowercased) level, and
// value counts for one chosen field, each split into what is recent and what
// is everything.
//
// Lines and Parsed are of the lines in view, the filtered ones while a
// filter is set. Each list is counted the same way but for the filter's
// terms on its own field, which it sets aside: under `level=error` the
// levels list still holds every level the rest of the filter lets through,
// with the chosen ones marked by the caller, so the next level can be picked
// from the list the first one was. What the filter says about the other
// fields still narrows it — "how many of these are errors" under
// `route=/login` is answered.
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

// ComputeStats aggregates the tail; see Stats for which lines each count is
// over. field chooses the Values counts; empty leaves them out. now is what
// "recent" is measured back from.
func (s *Store) ComputeStats(field string, now time.Time) Stats {
	stats := Stats{Field: field, Tail: s.buffer.Len()}
	clock, window, at := s.recency(now)
	stats.Clock, stats.Recent = clock, window
	cutoff := now.Add(-window)

	levels := make(map[string]Count)
	values := make(map[string]Count)
	for index := range s.buffer.Len() {
		line, _ := s.buffer.Get(index)
		if s.filter == nil || s.filter.Matches(line.Record) {
			stats.Lines++
			if line.Record != nil {
				stats.Parsed++
			}
		}
		if line.Record == nil {
			continue
		}
		recent := !at[index].IsZero() && !at[index].Before(cutoff)
		if level, ok := line.Record.Level(); ok && s.filter.MatchesExcept(line.Record, LevelKey) {
			count(levels, strings.ToLower(level), recent)
		}
		if field != "" && s.filter.MatchesExcept(line.Record, field) {
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

// facetDistinct is how many distinct values a field is tracked up to. A field
// past it is an identifier or a free text, not something to count by.
const facetDistinct = 64

// Fields lists the fields of the tail's records worth counting by, best
// first, for the stats panel to step through. The well-known ones are left
// out: the level has a list of its own, and a message or a timestamp is
// different on every line.
//
// The best are the facets — fields whose values repeat, at least two of
// them and on average four lines a value, like a route or a status — ranked
// by how many lines carry them. The rest follow by the same ranking, so
// every field can still be reached.
func (s *Store) Fields() []string {
	type seen struct {
		lines  int
		values map[string]struct{}
	}
	fields := make(map[string]*seen)
	for index := range s.buffer.Len() {
		line, _ := s.buffer.Get(index)
		if line.Record == nil {
			continue
		}
		for _, field := range line.Record.Fields() {
			if IsWellKnownKey(field.Key) {
				continue
			}
			entry := fields[field.Key]
			if entry == nil {
				entry = &seen{values: make(map[string]struct{})}
				fields[field.Key] = entry
			}
			entry.lines++
			if len(entry.values) <= facetDistinct {
				entry.values[field.Value] = struct{}{}
			}
		}
	}
	facet := func(key string) bool {
		entry := fields[key]
		distinct := len(entry.values)
		return distinct >= 2 && distinct <= facetDistinct && distinct*4 <= entry.lines
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if facetA, facetB := facet(a), facet(b); facetA != facetB {
			if facetA {
				return -1
			}
			return 1
		}
		if fields[a].lines != fields[b].lines {
			return fields[b].lines - fields[a].lines
		}
		return strings.Compare(a, b)
	})
	return keys
}

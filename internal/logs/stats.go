package logs

import (
	"slices"
	"strings"
)

// Count is one aggregation bucket.
type Count struct {
	Key string
	N   int
}

// Stats are aggregations over the lines currently held in a store's tail:
// counts by (lowercased) level, and value counts for one chosen field.
//
// They are recomputed on demand over the whole tail rather than maintained
// incrementally: at the tail's bounded size (10k lines) a recompute is
// well under a millisecond, which beats carrying add/remove symmetry
// invariants through every buffer mutation.
type Stats struct {
	Total  int
	Parsed int
	Levels []Count
	// Field is the field Values counts; empty means none chosen.
	Field  string
	Values []Count
}

// ComputeStats aggregates the whole tail of the store, regardless of any
// filter. field chooses the Values counts; empty leaves them out.
func (s *Store) ComputeStats(field string) Stats {
	stats := Stats{Field: field}
	levels := make(map[string]int)
	values := make(map[string]int)
	for i := range s.buffer.Len() {
		line, _ := s.buffer.Get(i)
		stats.Total++
		if line.Record == nil {
			continue
		}
		stats.Parsed++
		if level, ok := line.Record.Level(); ok {
			levels[strings.ToLower(level)]++
		}
		if field != "" {
			if value, ok := line.Record.Get(field); ok {
				values[value]++
			}
		}
	}
	stats.Levels = sortedCounts(levels)
	stats.Values = sortedCounts(values)
	return stats
}

// sortedCounts orders buckets highest first, ties breaking alphabetically.
func sortedCounts(m map[string]int) []Count {
	counts := make([]Count, 0, len(m))
	for key, n := range m {
		counts = append(counts, Count{Key: key, N: n})
	}
	slices.SortFunc(counts, func(a, b Count) int {
		if a.N != b.N {
			return b.N - a.N
		}
		return strings.Compare(a.Key, b.Key)
	})
	return counts
}

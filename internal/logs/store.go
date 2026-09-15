package logs

import "time"

// Store is the log engine's state for one followed stream: a bounded tail
// of parsed lines, an optional field filter narrowing the visible view,
// and search over the visible view.
//
// All indices exposed by this type (Line, Search*, the drop count Push
// returns) refer to the visible view: the filtered subsequence when a
// filter is set, the whole tail otherwise. They shift down when a full
// buffer drops a visible line.
type Store struct {
	buffer *TailBuffer[LogLine]
	// baseSeq is the sequence number of the oldest line still buffered;
	// the line at buffer index i has sequence baseSeq + i.
	baseSeq uint64
	filter  *Filter
	// visible holds the sequence numbers of the lines matching filter,
	// oldest first. Maintained only while a filter is set.
	visible []uint64
	// parsed counts buffered lines carrying a JSONL record, for the
	// structured-stream detection heuristic.
	parsed int
}

// detectMinParsed is the minimum parsed lines before auto-detection may
// call a stream JSONL, so one lucky line cannot flip the view.
const detectMinParsed = 3

func NewStore(capacity int) *Store {
	return &Store{buffer: NewTailBuffer[LogLine](capacity)}
}

// Push parses and appends one raw line that arrived just now; it returns how
// many lines left the visible view from the top (0 or 1), for scroll
// compensation.
func (s *Store) Push(raw string) int {
	return s.PushAt(raw, time.Now())
}

// PushAt is Push for a line that arrived at a given moment.
func (s *Store) PushAt(raw string, arrived time.Time) int {
	line := ParseLine(raw)
	line.Arrived = arrived
	if line.Record != nil {
		s.parsed++
	}
	seq := s.baseSeq + uint64(s.buffer.Len())
	matches := s.filter != nil && s.filter.Matches(line.Record)

	dropped, droppedOK := s.buffer.Push(line)
	if droppedOK {
		if dropped.Record != nil {
			s.parsed--
		}
		s.baseSeq++
	}

	if s.filter == nil {
		if droppedOK {
			return 1
		}
		return 0
	}
	if matches {
		s.visible = append(s.visible, seq)
	}
	if len(s.visible) > 0 && s.visible[0] < s.baseSeq {
		s.visible = s.visible[1:]
		return 1
	}
	return 0
}

// SetFilter sets or clears the field filter, rebuilding the visible view.
func (s *Store) SetFilter(filter *Filter) {
	s.filter = filter
	s.visible = nil
	if filter == nil {
		return
	}
	for i := range s.buffer.Len() {
		line, _ := s.buffer.Get(i)
		if filter.Matches(line.Record) {
			s.visible = append(s.visible, s.baseSeq+uint64(i))
		}
	}
}

// Filter returns the active filter, nil when none is set.
func (s *Store) Filter() *Filter {
	return s.filter
}

// LooksStructured is a heuristic JSONL detection over the current tail:
// most lines parse, with a small floor.
func (s *Store) LooksStructured() bool {
	return s.parsed >= detectMinParsed && s.parsed*2 > s.buffer.Len()
}

// Len is the number of lines in the visible view.
func (s *Store) Len() int {
	if s.filter != nil {
		return len(s.visible)
	}
	return s.buffer.Len()
}

// Total is the number of lines in the whole tail, regardless of the
// filter.
func (s *Store) Total() int {
	return s.buffer.Len()
}

// Line returns the line at visible index i (0 = oldest visible).
func (s *Store) Line(i int) (LogLine, bool) {
	if s.filter != nil {
		if i < 0 || i >= len(s.visible) {
			return LogLine{}, false
		}
		return s.buffer.Get(int(s.visible[i] - s.baseSeq))
	}
	return s.buffer.Get(i)
}

// SearchNext returns the first visible line at or after from whose raw
// text contains query (ASCII case insensitive), wrapping around to the
// start.
func (s *Store) SearchNext(query string, from int) (int, bool) {
	return s.search(query, from, 1)
}

// SearchPrev returns the first visible line at or before from containing
// query, wrapping around to the end.
func (s *Store) SearchPrev(query string, from int) (int, bool) {
	return s.search(query, from, -1)
}

func (s *Store) search(query string, from, direction int) (int, bool) {
	n := s.Len()
	if n == 0 {
		return 0, false
	}
	from = min(from, n-1)
	for step := range n {
		i := ((from+direction*step)%n + n) % n
		if line, ok := s.Line(i); ok && FindASCIICI(line.Raw, query) >= 0 {
			return i, true
		}
	}
	return 0, false
}

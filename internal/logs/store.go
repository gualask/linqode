package logs

// Store is the log engine's state for one followed stream: a bounded tail
// of lines with wrap-around search. Indices refer to the tail, 0 being the
// oldest buffered line; they shift down when a full buffer drops a line.
type Store struct {
	buffer *TailBuffer[string]
}

func NewStore(capacity int) *Store {
	return &Store{buffer: NewTailBuffer[string](capacity)}
}

// Push appends one raw line; it returns how many lines left the view from
// the top (0 or 1), for scroll compensation.
func (s *Store) Push(raw string) int {
	if _, dropped := s.buffer.Push(raw); dropped {
		return 1
	}
	return 0
}

func (s *Store) Len() int {
	return s.buffer.Len()
}

// Line returns the line at index i (0 = oldest).
func (s *Store) Line(i int) (string, bool) {
	return s.buffer.Get(i)
}

// SearchNext returns the first line at or after from whose text contains
// query (ASCII case insensitive), wrapping around to the start.
func (s *Store) SearchNext(query string, from int) (int, bool) {
	return s.search(query, from, 1)
}

// SearchPrev returns the first line at or before from containing query,
// wrapping around to the end.
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
		if line, ok := s.buffer.Get(i); ok && FindASCIICI(line, query) >= 0 {
			return i, true
		}
	}
	return 0, false
}

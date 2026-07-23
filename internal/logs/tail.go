package logs

// TailBuffer is a bounded ring buffer of the most recent log lines. When
// full, pushing drops the oldest line, shifting every index down by one —
// callers keeping index-based state (scroll position, current match)
// compensate using the drop Push reports.
type TailBuffer[T any] struct {
	items    []T // ring storage; grows up to capacity, then wraps
	capacity int
	start    int
	size     int
}

// NewTailBuffer creates a buffer holding at most capacity items. It panics
// if capacity is not positive.
func NewTailBuffer[T any](capacity int) *TailBuffer[T] {
	if capacity <= 0 {
		panic("TailBuffer capacity must be positive")
	}
	return &TailBuffer[T]{capacity: capacity}
}

// Push appends an item; it returns the oldest item and true when one had to
// be dropped.
func (b *TailBuffer[T]) Push(item T) (dropped T, ok bool) {
	if b.size < b.capacity {
		// Still filling: storage only grows before the first drop, so
		// start is 0 and the ring is a plain slice.
		b.items = append(b.items, item)
		b.size++
		return dropped, false
	}
	dropped = b.items[b.start]
	b.items[b.start] = item
	b.start = (b.start + 1) % len(b.items)
	return dropped, true
}

// Len is the number of buffered items.
func (b *TailBuffer[T]) Len() int {
	return b.size
}

// Get returns the item at index i, 0 being the oldest.
func (b *TailBuffer[T]) Get(i int) (item T, ok bool) {
	if i < 0 || i >= b.size {
		return item, false
	}
	return b.items[(b.start+i)%len(b.items)], true
}

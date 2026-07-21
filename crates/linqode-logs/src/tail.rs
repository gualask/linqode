use std::collections::VecDeque;

/// A bounded buffer of the most recent log lines. When full, pushing drops
/// the oldest line, shifting every index down by one — callers keeping
/// index-based state (scroll position, current match) compensate using the
/// dropped line [`TailBuffer::push`] returns.
#[derive(Debug)]
pub struct TailBuffer<T> {
    lines: VecDeque<T>,
    capacity: usize,
}

impl<T> TailBuffer<T> {
    /// # Panics
    /// If `capacity` is zero.
    pub fn new(capacity: usize) -> Self {
        assert!(capacity > 0, "TailBuffer capacity must be positive");
        Self {
            lines: VecDeque::with_capacity(capacity.min(1024)),
            capacity,
        }
    }

    /// Appends a line; returns the oldest line when it had to be dropped.
    pub fn push(&mut self, line: T) -> Option<T> {
        self.lines.push_back(line);
        if self.lines.len() > self.capacity {
            self.lines.pop_front()
        } else {
            None
        }
    }

    pub fn len(&self) -> usize {
        self.lines.len()
    }

    pub fn is_empty(&self) -> bool {
        self.lines.is_empty()
    }

    pub fn get(&self, index: usize) -> Option<&T> {
        self.lines.get(index)
    }

    /// Lines from index `start` (0 = oldest) to the newest.
    pub fn iter_from(&self, start: usize) -> impl Iterator<Item = &T> {
        self.lines.iter().skip(start)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn drops_oldest_when_full() {
        let mut buffer = TailBuffer::new(2);
        assert_eq!(buffer.push("a"), None);
        assert_eq!(buffer.push("b"), None);
        assert_eq!(buffer.push("c"), Some("a"));
        assert_eq!(buffer.iter_from(0).copied().collect::<Vec<_>>(), ["b", "c"]);
    }

    #[test]
    fn get_and_iter_from_index_from_oldest() {
        let mut buffer = TailBuffer::new(10);
        buffer.push(1);
        buffer.push(2);
        buffer.push(3);
        assert_eq!(buffer.get(1), Some(&2));
        assert_eq!(buffer.get(3), None);
        assert_eq!(buffer.iter_from(2).copied().collect::<Vec<_>>(), [3]);
    }
}

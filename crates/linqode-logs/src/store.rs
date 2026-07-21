use std::collections::VecDeque;

use crate::agg::Aggregator;
use crate::filter::Filter;
use crate::record::LogLine;
use crate::tail::TailBuffer;
use crate::find_ascii_ci;

/// The log engine's state for one followed stream: a bounded tail of parsed
/// lines, an optional field filter narrowing the visible view, live
/// aggregations over the whole tail, and search over the visible view.
///
/// All indices exposed by this type (`line`, `search_*`, the drop count
/// `push` returns) refer to the **visible view**: the filtered subsequence
/// when a filter is set, the whole tail otherwise.
#[derive(Debug)]
pub struct LogStore {
    buffer: TailBuffer<LogLine>,
    /// Sequence number of the oldest line still in `buffer`; the line at
    /// buffer index `i` has sequence `base_seq + i`.
    base_seq: u64,
    filter: Option<Filter>,
    /// Sequence numbers of the lines matching `filter`, oldest first.
    /// Maintained only while a filter is set.
    visible: VecDeque<u64>,
    aggregator: Aggregator,
}

/// Minimum parsed lines before auto-detection may call a stream JSONL.
const DETECT_MIN_PARSED: usize = 3;

impl LogStore {
    pub fn new(capacity: usize) -> Self {
        Self {
            buffer: TailBuffer::new(capacity),
            base_seq: 0,
            filter: None,
            visible: VecDeque::new(),
            aggregator: Aggregator::new(None),
        }
    }

    /// Parses and appends one raw line; returns how many lines left the
    /// visible view from the top (0 or 1), for scroll compensation.
    pub fn push(&mut self, raw: String) -> usize {
        let line = LogLine::parse(raw);
        self.aggregator.add(line.record.as_ref());
        let seq = self.base_seq + self.buffer.len() as u64;
        let matches = self
            .filter
            .as_ref()
            .is_some_and(|filter| filter.matches(line.record.as_ref()));
        let dropped = self.buffer.push(line);
        if let Some(dropped) = &dropped {
            self.aggregator.remove(dropped.record.as_ref());
            self.base_seq += 1;
        }
        if self.filter.is_none() {
            return usize::from(dropped.is_some());
        }
        if matches {
            self.visible.push_back(seq);
        }
        if self.visible.front().is_some_and(|&s| s < self.base_seq) {
            self.visible.pop_front();
            1
        } else {
            0
        }
    }

    /// Sets or clears the field filter, rebuilding the visible view.
    pub fn set_filter(&mut self, filter: Option<Filter>) {
        self.filter = filter;
        self.visible.clear();
        if let Some(filter) = &self.filter {
            for (i, line) in self.buffer.iter_from(0).enumerate() {
                if filter.matches(line.record.as_ref()) {
                    self.visible.push_back(self.base_seq + i as u64);
                }
            }
        }
    }

    pub fn filter(&self) -> Option<&Filter> {
        self.filter.as_ref()
    }

    /// Changes the field tracked by [`Aggregator::top_values`], recounting
    /// the current tail.
    pub fn set_top_field(&mut self, field: Option<String>) {
        let mut aggregator = Aggregator::new(field);
        for line in self.buffer.iter_from(0) {
            aggregator.add(line.record.as_ref());
        }
        self.aggregator = aggregator;
    }

    pub fn stats(&self) -> &Aggregator {
        &self.aggregator
    }

    /// Heuristic JSONL detection over the current tail: most lines parse,
    /// with a small floor so one lucky line cannot flip the view.
    pub fn looks_structured(&self) -> bool {
        let parsed = self.aggregator.parsed();
        parsed >= DETECT_MIN_PARSED && parsed * 2 > self.aggregator.total()
    }

    /// Lines in the visible view.
    pub fn len(&self) -> usize {
        match &self.filter {
            Some(_) => self.visible.len(),
            None => self.buffer.len(),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Lines in the whole tail, regardless of the filter.
    pub fn total(&self) -> usize {
        self.buffer.len()
    }

    /// Line at visible index `index` (0 = oldest visible).
    pub fn line(&self, index: usize) -> Option<&LogLine> {
        let buffer_index = match &self.filter {
            Some(_) => (*self.visible.get(index)? - self.base_seq) as usize,
            None => index,
        };
        self.buffer.get(buffer_index)
    }

    /// Visible lines from index `start` to the newest.
    pub fn iter_from(&self, start: usize) -> impl Iterator<Item = &LogLine> {
        (start..self.len()).map_while(|index| self.line(index))
    }

    /// First visible line at or after `from` whose raw text contains
    /// `query` (ASCII case insensitive), wrapping around to the start.
    pub fn search_next(&self, query: &str, from: usize) -> Option<usize> {
        self.search(query, from, 1)
    }

    /// First visible line at or before `from` containing `query`, wrapping
    /// around to the end.
    pub fn search_prev(&self, query: &str, from: usize) -> Option<usize> {
        self.search(query, from, -1)
    }

    fn search(&self, query: &str, from: usize, direction: i64) -> Option<usize> {
        let len = self.len();
        if len == 0 {
            return None;
        }
        let from = from.min(len - 1) as i64;
        (0..len as i64)
            .map(|step| (from + direction * step).rem_euclid(len as i64) as usize)
            .find(|&i| {
                self.line(i)
                    .is_some_and(|line| find_ascii_ci(&line.raw, query).is_some())
            })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn store(lines: &[&str]) -> LogStore {
        let mut store = LogStore::new(100);
        for line in lines {
            store.push((*line).to_string());
        }
        store
    }

    fn filter(expr: &str) -> Option<Filter> {
        Filter::parse(expr).unwrap()
    }

    #[test]
    fn unfiltered_view_is_the_whole_tail() {
        let store = store(&["a", "b"]);
        assert_eq!(store.len(), 2);
        assert_eq!(store.total(), 2);
        assert_eq!(store.line(0).unwrap().raw, "a");
        assert_eq!(
            store.iter_from(1).map(|l| l.raw.as_str()).collect::<Vec<_>>(),
            ["b"]
        );
    }

    #[test]
    fn drops_report_when_the_view_loses_its_top_line() {
        let mut store = LogStore::new(2);
        assert_eq!(store.push("a".into()), 0);
        assert_eq!(store.push("b".into()), 0);
        assert_eq!(store.push("c".into()), 1);
        assert_eq!(store.line(0).unwrap().raw, "b");
    }

    #[test]
    fn filter_narrows_view_and_rebuilds_on_change() {
        let mut store = store(&[
            r#"{"level":"info","msg":"one"}"#,
            "plain text",
            r#"{"level":"error","msg":"two"}"#,
            r#"{"level":"error","msg":"three"}"#,
        ]);
        store.set_filter(filter("level=error"));
        assert_eq!(store.len(), 2);
        assert_eq!(store.total(), 4);
        assert_eq!(store.line(0).unwrap().record.as_ref().unwrap().message(), Some("two"));
        store.set_filter(None);
        assert_eq!(store.len(), 4);
    }

    #[test]
    fn filtered_view_tracks_pushes_and_drops() {
        let mut store = LogStore::new(3);
        store.set_filter(filter("level=error"));
        assert_eq!(store.push(r#"{"level":"error","n":1}"#.into()), 0);
        assert_eq!(store.push(r#"{"level":"info"}"#.into()), 0);
        assert_eq!(store.push(r#"{"level":"error","n":2}"#.into()), 0);
        assert_eq!(store.len(), 2);
        // Buffer is full: the next push drops the first error line.
        assert_eq!(store.push(r#"{"level":"info"}"#.into()), 1);
        assert_eq!(store.len(), 1);
        assert_eq!(store.line(0).unwrap().record.as_ref().unwrap().get("n"), Some("2"));
        // Dropping a non-matching line does not disturb the view.
        assert_eq!(store.push(r#"{"level":"info"}"#.into()), 0);
        assert_eq!(store.len(), 1);
    }

    #[test]
    fn search_runs_over_the_visible_view() {
        let mut store = store(&[
            r#"{"level":"error","msg":"alpha"}"#,
            r#"{"level":"info","msg":"alpha"}"#,
            r#"{"level":"error","msg":"beta"}"#,
        ]);
        assert_eq!(store.search_next("alpha", 0), Some(0));
        store.set_filter(filter("level=error"));
        // Visible view is [alpha(error), beta(error)]: index 1 is beta.
        assert_eq!(store.search_next("beta", 0), Some(1));
        assert_eq!(store.search_next("alpha", 1), Some(0)); // wrapped
        assert_eq!(store.search_prev("beta", 0), Some(1)); // wrapped
        assert_eq!(store.search_next("info", 0), None);
    }

    #[test]
    fn detects_mostly_jsonl_streams() {
        let mut store = LogStore::new(100);
        assert!(!store.looks_structured());
        store.push(r#"{"level":"info"}"#.into());
        store.push(r#"{"level":"info"}"#.into());
        assert!(!store.looks_structured()); // below the floor
        store.push(r#"{"level":"info"}"#.into());
        assert!(store.looks_structured());
        for _ in 0..3 {
            store.push("plain".into());
        }
        assert!(!store.looks_structured()); // no longer a majority
    }

    #[test]
    fn top_field_recounts_the_current_tail() {
        let mut store = store(&[
            r#"{"path":"/a"}"#,
            r#"{"path":"/b"}"#,
            r#"{"path":"/a"}"#,
        ]);
        store.set_top_field(Some("path".into()));
        assert_eq!(store.stats().top_values(5), [("/a", 2), ("/b", 1)]);
        assert_eq!(store.stats().total(), 3);
        store.push(r#"{"path":"/b"}"#.into());
        assert_eq!(store.stats().top_values(5), [("/a", 2), ("/b", 2)]);
    }
}

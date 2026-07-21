use std::collections::HashMap;

use crate::record::Record;

/// Live aggregations over the lines currently held in a tail buffer:
/// counts by (lowercased) level, and value counts for one chosen field.
/// Callers keep it in sync by mirroring buffer pushes with [`Aggregator::add`]
/// and drops with [`Aggregator::remove`].
#[derive(Debug, Default)]
pub struct Aggregator {
    total: usize,
    parsed: usize,
    levels: HashMap<String, usize>,
    field: Option<String>,
    values: HashMap<String, usize>,
}

impl Aggregator {
    pub fn new(field: Option<String>) -> Self {
        Self {
            field,
            ..Self::default()
        }
    }

    /// The field whose values [`Aggregator::top_values`] counts.
    pub fn field(&self) -> Option<&str> {
        self.field.as_deref()
    }

    /// Accounts for one line entering the buffer.
    pub fn add(&mut self, record: Option<&Record>) {
        self.update(record, 1);
    }

    /// Accounts for one line dropped from the buffer.
    pub fn remove(&mut self, record: Option<&Record>) {
        self.update(record, -1);
    }

    fn update(&mut self, record: Option<&Record>, delta: isize) {
        Self::bump(&mut self.total, delta);
        let Some(record) = record else {
            return;
        };
        Self::bump(&mut self.parsed, delta);
        if let Some(level) = record.level() {
            Self::bump_entry(&mut self.levels, &level.to_ascii_lowercase(), delta);
        }
        if let Some(value) = self.field.as_deref().and_then(|field| record.get(field)) {
            let value = value.to_string();
            Self::bump_entry(&mut self.values, &value, delta);
        }
    }

    fn bump(slot: &mut usize, delta: isize) {
        *slot = slot.saturating_add_signed(delta);
    }

    fn bump_entry(map: &mut HashMap<String, usize>, key: &str, delta: isize) {
        match map.get_mut(key) {
            Some(count) => {
                *count = count.saturating_add_signed(delta);
                if *count == 0 {
                    map.remove(key);
                }
            }
            None if delta > 0 => {
                map.insert(key.to_string(), delta as usize);
            }
            None => {}
        }
    }

    /// Lines currently accounted for.
    pub fn total(&self) -> usize {
        self.total
    }

    /// How many of them parsed as JSONL records.
    pub fn parsed(&self) -> usize {
        self.parsed
    }

    /// Level counts, highest first (ties break alphabetically).
    pub fn level_counts(&self) -> Vec<(&str, usize)> {
        Self::sorted(&self.levels)
    }

    /// The `n` most frequent values of the chosen field, highest first.
    pub fn top_values(&self, n: usize) -> Vec<(&str, usize)> {
        let mut sorted = Self::sorted(&self.values);
        sorted.truncate(n);
        sorted
    }

    fn sorted(map: &HashMap<String, usize>) -> Vec<(&str, usize)> {
        let mut entries: Vec<(&str, usize)> =
            map.iter().map(|(k, v)| (k.as_str(), *v)).collect();
        entries.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(b.0)));
        entries
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn record(json: &str) -> Record {
        Record::parse(json).unwrap()
    }

    #[test]
    fn counts_levels_case_folded_and_sorted() {
        let mut agg = Aggregator::new(None);
        agg.add(Some(&record(r#"{"level":"ERROR"}"#)));
        agg.add(Some(&record(r#"{"level":"error"}"#)));
        agg.add(Some(&record(r#"{"level":"info"}"#)));
        agg.add(None); // plain-text line
        assert_eq!(agg.total(), 4);
        assert_eq!(agg.parsed(), 3);
        assert_eq!(agg.level_counts(), [("error", 2), ("info", 1)]);
    }

    #[test]
    fn remove_reverses_add() {
        let mut agg = Aggregator::new(None);
        let error = record(r#"{"level":"error"}"#);
        agg.add(Some(&error));
        agg.add(Some(&record(r#"{"level":"info"}"#)));
        agg.remove(Some(&error));
        assert_eq!(agg.level_counts(), [("info", 1)]);
        assert_eq!(agg.total(), 1);
        assert_eq!(agg.parsed(), 1);
    }

    #[test]
    fn top_values_track_the_chosen_field() {
        let mut agg = Aggregator::new(Some("path".into()));
        for path in ["/a", "/b", "/a", "/c", "/a", "/b"] {
            agg.add(Some(&record(&format!(r#"{{"path":"{path}"}}"#))));
        }
        assert_eq!(agg.top_values(2), [("/a", 3), ("/b", 2)]);
        assert_eq!(agg.field(), Some("path"));
    }

    #[test]
    fn without_field_top_values_is_empty() {
        let mut agg = Aggregator::new(None);
        agg.add(Some(&record(r#"{"path":"/a"}"#)));
        assert!(agg.top_values(5).is_empty());
    }

    #[test]
    fn ties_break_alphabetically() {
        let mut agg = Aggregator::new(Some("k".into()));
        agg.add(Some(&record(r#"{"k":"b"}"#)));
        agg.add(Some(&record(r#"{"k":"a"}"#)));
        assert_eq!(agg.top_values(5), [("a", 1), ("b", 1)]);
    }
}

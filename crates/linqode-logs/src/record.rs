use serde_json::{Map, Value};

/// Keys probed, in order, by the well-known field accessors.
const LEVEL_KEYS: [&str; 4] = ["level", "lvl", "severity", "log.level"];
const MESSAGE_KEYS: [&str; 3] = ["msg", "message", "event"];
const TIME_KEYS: [&str; 4] = ["time", "ts", "timestamp", "@timestamp"];

/// One structured (JSONL) log record: the line's top-level JSON object
/// flattened into string key/value pairs. Nested objects join their keys
/// with `.` (`{"http":{"status":500}}` → `http.status=500`); strings keep
/// their content unquoted; every other value keeps its compact JSON form.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Record {
    fields: Vec<(String, String)>,
}

impl Record {
    /// Parses a line as a JSONL record. Only complete JSON objects qualify;
    /// bare arrays and scalars are valid JSON but not log records.
    pub fn parse(line: &str) -> Option<Self> {
        let trimmed = line.trim_ascii();
        if !trimmed.starts_with('{') {
            return None; // cheap reject before invoking the JSON parser
        }
        let Value::Object(map) = serde_json::from_str(trimmed).ok()? else {
            return None;
        };
        let mut fields = Vec::with_capacity(map.len());
        flatten(&map, "", &mut fields);
        Some(Self { fields })
    }

    /// Value of `key` (a flattened path like `http.status`), if present.
    pub fn get(&self, key: &str) -> Option<&str> {
        self.fields
            .iter()
            .find(|(k, _)| k == key)
            .map(|(_, v)| v.as_str())
    }

    /// All flattened fields, sorted by key (JSON object order).
    pub fn fields(&self) -> impl Iterator<Item = (&str, &str)> {
        self.fields.iter().map(|(k, v)| (k.as_str(), v.as_str()))
    }

    /// The record's severity, from the first present well-known level key.
    pub fn level(&self) -> Option<&str> {
        self.first_of(&LEVEL_KEYS)
    }

    /// The record's human message, from the first well-known message key.
    pub fn message(&self) -> Option<&str> {
        self.first_of(&MESSAGE_KEYS)
    }

    /// The record's timestamp, from the first well-known time key.
    pub fn timestamp(&self) -> Option<&str> {
        self.first_of(&TIME_KEYS)
    }

    /// True for keys consumed by the dedicated level/message/timestamp
    /// slots, so renderers can skip them when listing remaining fields.
    pub fn is_well_known(key: &str) -> bool {
        LEVEL_KEYS.contains(&key) || MESSAGE_KEYS.contains(&key) || TIME_KEYS.contains(&key)
    }

    fn first_of(&self, keys: &[&str]) -> Option<&str> {
        keys.iter().find_map(|key| self.get(key))
    }
}

fn flatten(map: &Map<String, Value>, prefix: &str, out: &mut Vec<(String, String)>) {
    for (key, value) in map {
        let path = if prefix.is_empty() {
            key.clone()
        } else {
            format!("{prefix}.{key}")
        };
        match value {
            Value::Object(nested) => flatten(nested, &path, out),
            Value::String(s) => out.push((path, s.clone())),
            other => out.push((path, other.to_string())),
        }
    }
}

/// One log line as stored by the engine: the raw text plus its parsed
/// record when the line is JSONL.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LogLine {
    pub raw: String,
    pub record: Option<Record>,
}

impl LogLine {
    pub fn parse(raw: String) -> Self {
        let record = Record::parse(&raw);
        Self { raw, record }
    }
}

impl AsRef<str> for LogLine {
    fn as_ref(&self) -> &str {
        &self.raw
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_flat_object() {
        let record = Record::parse(r#"{"level":"info","msg":"started","port":8080}"#).unwrap();
        assert_eq!(record.get("level"), Some("info"));
        assert_eq!(record.get("port"), Some("8080"));
        assert_eq!(record.level(), Some("info"));
        assert_eq!(record.message(), Some("started"));
    }

    #[test]
    fn flattens_nested_objects_and_keeps_arrays_as_json() {
        let record =
            Record::parse(r#"{"http":{"status":500,"path":"/x"},"tags":["a","b"]}"#).unwrap();
        assert_eq!(record.get("http.status"), Some("500"));
        assert_eq!(record.get("http.path"), Some("/x"));
        assert_eq!(record.get("tags"), Some(r#"["a","b"]"#));
    }

    #[test]
    fn rejects_non_objects_and_plain_text() {
        assert_eq!(Record::parse("plain text line"), None);
        assert_eq!(Record::parse("[1,2,3]"), None);
        assert_eq!(Record::parse("42"), None);
        assert_eq!(Record::parse("{broken"), None);
        assert_eq!(Record::parse(""), None);
    }

    #[test]
    fn tolerates_surrounding_whitespace() {
        assert!(Record::parse("  {\"a\":1}\t").is_some());
    }

    #[test]
    fn well_known_fallbacks_probe_in_order() {
        let record = Record::parse(r#"{"severity":"WARN","message":"m","ts":"t"}"#).unwrap();
        assert_eq!(record.level(), Some("WARN"));
        assert_eq!(record.message(), Some("m"));
        assert_eq!(record.timestamp(), Some("t"));
        assert!(Record::is_well_known("severity"));
        assert!(!Record::is_well_known("status"));
    }

    #[test]
    fn null_and_bool_values_stringify() {
        let record = Record::parse(r#"{"a":null,"b":true}"#).unwrap();
        assert_eq!(record.get("a"), Some("null"));
        assert_eq!(record.get("b"), Some("true"));
    }

    #[test]
    fn log_line_carries_record_only_for_jsonl() {
        assert!(LogLine::parse(r#"{"a":1}"#.into()).record.is_some());
        assert!(LogLine::parse("plain".into()).record.is_none());
    }
}

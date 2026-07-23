package logs

import (
	"encoding/json"
	"slices"
	"strings"
)

// Keys probed, in order, by the well-known field accessors.
var (
	levelKeys   = []string{"level", "lvl", "severity", "log.level"}
	messageKeys = []string{"msg", "message", "event"}
	timeKeys    = []string{"time", "ts", "timestamp", "@timestamp"}
)

// Field is one flattened key/value pair of a Record.
type Field struct {
	Key   string
	Value string
}

// Record is one structured (JSONL) log record: the line's top-level JSON
// object flattened into string key/value pairs, sorted by key. Nested
// objects join their keys with `.` ({"http":{"status":500}} →
// http.status=500); strings keep their content unquoted; every other value
// keeps its compact JSON form, with numeric literals verbatim.
type Record struct {
	fields []Field
}

// ParseRecord parses a line as a JSONL record, nil when it is not one.
// Only complete JSON objects qualify; bare arrays and scalars are valid
// JSON but not log records.
func ParseRecord(line string) *Record {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return nil // cheap reject before invoking the JSON parser
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber() // keep numeric literals verbatim
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil
	}
	if decoder.More() {
		return nil // trailing garbage: not a single JSON object
	}
	record := &Record{}
	flatten(object, "", &record.fields)
	slices.SortFunc(record.fields, func(a, b Field) int { return strings.Compare(a.Key, b.Key) })
	return record
}

func flatten(object map[string]any, prefix string, out *[]Field) {
	for key, value := range object {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		switch value := value.(type) {
		case map[string]any:
			flatten(value, path, out)
		case string:
			*out = append(*out, Field{Key: path, Value: value})
		default:
			raw, err := json.Marshal(value)
			if err != nil {
				continue
			}
			*out = append(*out, Field{Key: path, Value: string(raw)})
		}
	}
}

// Get returns the value of key (a flattened path like `http.status`) and
// whether it is present.
func (r *Record) Get(key string) (string, bool) {
	for _, f := range r.fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// Fields returns all flattened fields, sorted by key.
func (r *Record) Fields() []Field {
	return r.fields
}

// Level is the record's severity, from the first present well-known level
// key.
func (r *Record) Level() (string, bool) {
	return r.firstOf(levelKeys)
}

// Message is the record's human message, from the first well-known message
// key.
func (r *Record) Message() (string, bool) {
	return r.firstOf(messageKeys)
}

// Timestamp is the record's timestamp, from the first well-known time key.
func (r *Record) Timestamp() (string, bool) {
	return r.firstOf(timeKeys)
}

// IsWellKnownKey is true for keys consumed by the dedicated
// level/message/timestamp slots, so renderers can skip them when listing
// remaining fields.
func IsWellKnownKey(key string) bool {
	return slices.Contains(levelKeys, key) ||
		slices.Contains(messageKeys, key) ||
		slices.Contains(timeKeys, key)
}

func (r *Record) firstOf(keys []string) (string, bool) {
	for _, key := range keys {
		if value, ok := r.Get(key); ok {
			return value, true
		}
	}
	return "", false
}

// LogLine is one line as stored by the engine: the raw text plus its
// parsed record when the line is JSONL.
type LogLine struct {
	Raw    string
	Record *Record
}

// ParseLine wraps a raw line, attaching its record when it parses.
func ParseLine(raw string) LogLine {
	return LogLine{Raw: raw, Record: ParseRecord(raw)}
}

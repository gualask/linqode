package logs

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
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

// timeLayouts are the written timestamps a record's time is read from. Every
// one of them carries a zone. A timestamp without one is left unread rather
// than guessed at: which zone a server's logger meant is not in the line,
// and a wrong guess does not misplace a record by a little — it moves every
// one of them by hours, which on a timeline is the difference between "just
// now" and "before lunch".
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999Z0700",
	// Go's own Time.String, which a logger printing a time.Time produces.
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// Time is the record's timestamp as a point in time, when it can be read as
// one: a written timestamp that carries its zone, or a number of seconds,
// milliseconds, microseconds or nanoseconds since the epoch — which of those
// is decided by its size, since the loggers that write them do not say.
//
// A number that reads as a time before 2000 or after 2200 is not taken for
// one. Some loggers put seconds since the process started in `ts`, and 12.5
// seconds after 1970 is not when anything in this log happened.
func (r *Record) Time() (time.Time, bool) {
	text, ok := r.Timestamp()
	if !ok {
		return time.Time{}, false
	}
	text = strings.TrimSpace(text)
	if at, ok := parseEpoch(text); ok {
		return at, true
	}
	for _, layout := range timeLayouts {
		if at, err := time.Parse(layout, text); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

func parseEpoch(text string) (time.Time, bool) {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return time.Time{}, false
	}
	seconds := value
	switch {
	case value >= 1e17:
		seconds = value / 1e9
	case value >= 1e14:
		seconds = value / 1e6
	case value >= 1e11:
		seconds = value / 1e3
	}
	at := time.Unix(0, int64(seconds*1e9)).UTC()
	if at.Year() < 2000 || at.Year() > 2200 {
		return time.Time{}, false
	}
	return at, true
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
	// Logged is when the record says it was written, zero when it says
	// nothing readable. Read once here rather than at every render: a
	// timeline over ten thousand lines is redrawn ten times a second.
	Logged time.Time
	// Arrived is when the line reached this client, which every line has.
	Arrived time.Time
}

// ParseLine wraps a raw line, attaching its record and its logged time when
// it has them.
func ParseLine(raw string) LogLine {
	line := LogLine{Raw: raw, Record: ParseRecord(raw)}
	if line.Record != nil {
		if at, ok := line.Record.Time(); ok {
			line.Logged = at
		}
	}
	return line
}

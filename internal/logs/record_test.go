package logs

import (
	"testing"
	"time"
)

func mustGet(t *testing.T, r *Record, key string) string {
	t.Helper()
	value, ok := r.Get(key)
	if !ok {
		t.Fatalf("key %q missing", key)
	}
	return value
}

func TestParsesFlatObject(t *testing.T) {
	r := ParseRecord(`{"level":"info","msg":"started","port":8080}`)
	if r == nil {
		t.Fatal("no record")
	}
	if got := mustGet(t, r, "level"); got != "info" {
		t.Errorf("level %q", got)
	}
	if got := mustGet(t, r, "port"); got != "8080" {
		t.Errorf("port %q", got)
	}
	if level, _ := r.Level(); level != "info" {
		t.Errorf("Level() %q", level)
	}
	if msg, _ := r.Message(); msg != "started" {
		t.Errorf("Message() %q", msg)
	}
}

func TestFlattensNestedObjectsAndKeepsArraysAsJSON(t *testing.T) {
	r := ParseRecord(`{"http":{"status":500,"path":"/x"},"tags":["a","b"]}`)
	if r == nil {
		t.Fatal("no record")
	}
	if got := mustGet(t, r, "http.status"); got != "500" {
		t.Errorf("http.status %q", got)
	}
	if got := mustGet(t, r, "http.path"); got != "/x" {
		t.Errorf("http.path %q", got)
	}
	if got := mustGet(t, r, "tags"); got != `["a","b"]` {
		t.Errorf("tags %q", got)
	}
}

func TestRejectsNonObjectsAndPlainText(t *testing.T) {
	for _, line := range []string{"plain text line", "[1,2,3]", "42", "{broken", "", `{"a":1} extra`} {
		if ParseRecord(line) != nil {
			t.Errorf("should reject %q", line)
		}
	}
}

func TestToleratesSurroundingWhitespace(t *testing.T) {
	if ParseRecord("  {\"a\":1}\t") == nil {
		t.Error("whitespace-wrapped object rejected")
	}
}

func TestWellKnownFallbacksProbeInOrder(t *testing.T) {
	r := ParseRecord(`{"severity":"WARN","message":"m","ts":"t"}`)
	if level, _ := r.Level(); level != "WARN" {
		t.Errorf("level %q", level)
	}
	if msg, _ := r.Message(); msg != "m" {
		t.Errorf("message %q", msg)
	}
	if ts, _ := r.Timestamp(); ts != "t" {
		t.Errorf("timestamp %q", ts)
	}
	if !IsWellKnownKey("severity") {
		t.Error("severity should be well-known")
	}
	if IsWellKnownKey("status") {
		t.Error("status should not be well-known")
	}
}

func TestNullAndBoolValuesStringify(t *testing.T) {
	r := ParseRecord(`{"a":null,"b":true}`)
	if got := mustGet(t, r, "a"); got != "null" {
		t.Errorf("a %q", got)
	}
	if got := mustGet(t, r, "b"); got != "true" {
		t.Errorf("b %q", got)
	}
}

func TestNumericLiteralsStayVerbatim(t *testing.T) {
	r := ParseRecord(`{"big":9007199254740993,"f":1.50}`)
	if got := mustGet(t, r, "big"); got != "9007199254740993" {
		t.Errorf("big %q", got)
	}
	if got := mustGet(t, r, "f"); got != "1.50" {
		t.Errorf("f %q", got)
	}
}

func TestLogLineCarriesRecordOnlyForJSONL(t *testing.T) {
	if ParseLine(`{"a":1}`).Record == nil {
		t.Error("JSONL line has no record")
	}
	if ParseLine("plain").Record != nil {
		t.Error("plain line has a record")
	}
}

var noon = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

// A written timestamp is read only when it says which zone it is in, and a
// number only when it lands somewhere a log could have been written.
func TestRecordTimeReadsZonedAndEpochTimestamps(t *testing.T) {
	cases := []struct {
		line string
		want time.Time
	}{
		{`{"time":"2026-09-15T12:00:00Z"}`, noon},
		{`{"time":"2026-09-15T14:00:00.250+02:00"}`, noon.Add(250 * time.Millisecond)},
		{`{"ts":"2026-09-15 12:00:00Z"}`, noon},
		{`{"@timestamp":"2026-09-15T12:00:00+0000"}`, noon},
		{`{"time":"2026-09-15 12:00:00 +0000 UTC"}`, noon},
		{`{"ts":1789473600.5}`, noon.Add(500 * time.Millisecond)},
		{`{"time":1789473600000}`, noon},
		{`{"time":1789473600000000}`, noon},
		{`{"time":1789473600000000000}`, noon},
		{`{"ts":"1789473600"}`, noon},
	}
	for _, c := range cases {
		got, ok := ParseRecord(c.line).Time()
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s read as %v (%v), want %v", c.line, got, ok, c.want)
		}
	}
	for _, line := range []string{
		`{"time":"2026-09-15 12:00:00"}`, // no zone: a guess would move it by hours
		`{"time":"12:00:01"}`,
		`{"ts":12.5}`, // seconds since the process started
		`{"time":"yesterday"}`,
		`{"msg":"no time at all"}`,
	} {
		if got, ok := ParseRecord(line).Time(); ok {
			t.Errorf("%s read as %v, want no time", line, got)
		}
	}
}

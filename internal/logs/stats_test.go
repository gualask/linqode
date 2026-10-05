package logs

import "testing"

// Loggers spell a level many ways, and pino and bunyan write it as a number.
// A line whose level is not recognised is drawn like an info line, so every
// spelling missed here is an error that does not look like one.
func TestSeverityReadsEverySpelling(t *testing.T) {
	for level, want := range map[string]Severity{
		"error": SeverityError, "ERROR": SeverityError, "err": SeverityError,
		"ERR": SeverityError, "eror": SeverityError, "fatal": SeverityError,
		"FTL": SeverityError, "crit": SeverityError, "critical": SeverityError,
		"CRITICAL": SeverityError, "panic": SeverityError, "dpanic": SeverityError,
		"emerg": SeverityError, "emergency": SeverityError, "alert": SeverityError,
		"SEVERE": SeverityError,

		"warn": SeverityWarning, "WARNING": SeverityWarning, "wrn": SeverityWarning,

		"info": SeverityNone, "inf": SeverityNone, "debug": SeverityNone,
		"dbg": SeverityNone, "trace": SeverityNone, "notice": SeverityNone,
		"": SeverityNone,

		// pino and bunyan.
		"60": SeverityError, "50": SeverityError, "40": SeverityWarning,
		"30": SeverityNone, "20": SeverityNone, "10": SeverityNone,
		" 50 ": SeverityError, "45": SeverityWarning, "50.0": SeverityError,
		// On no scale those two use: syslog's run the other way.
		"3": SeverityNone, "0": SeverityNone, "-50": SeverityNone,
		"NaN": SeverityNone, "Inf": SeverityNone,
	} {
		if got := SeverityOf(level); got != want {
			t.Errorf("SeverityOf(%q) = %v, want %v", level, got, want)
		}
	}
}

// pino writes the level as a JSON number, which the record keeps verbatim.
func TestANumericLevelInARecordIsClassified(t *testing.T) {
	for line, want := range map[string]Severity{
		`{"level":50,"msg":"boom"}`: SeverityError,
		`{"level":40,"msg":"hmm"}`:  SeverityWarning,
		`{"level":30,"msg":"ok"}`:   SeverityNone,
		`{"level":"60","msg":"x"}`:  SeverityError,
	} {
		record := ParseRecord(line)
		if record == nil {
			t.Fatalf("%s did not parse", line)
		}
		level, ok := record.Level()
		if !ok {
			t.Fatalf("%s has no level", line)
		}
		if got := SeverityOf(level); got != want {
			t.Errorf("%s: SeverityOf(%q) = %v, want %v", line, level, got, want)
		}
	}
}

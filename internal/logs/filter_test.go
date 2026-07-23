package logs

import "testing"

func record(t *testing.T, json string) *Record {
	t.Helper()
	r := ParseRecord(json)
	if r == nil {
		t.Fatalf("cannot parse %q", json)
	}
	return r
}

func filter(t *testing.T, expr string) *Filter {
	t.Helper()
	f, err := ParseFilter(expr)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatalf("empty filter for %q", expr)
	}
	return f
}

func TestEmptyExpressionMeansNoFilter(t *testing.T) {
	for _, expr := range []string{"", "   "} {
		f, err := ParseFilter(expr)
		if err != nil || f != nil {
			t.Errorf("ParseFilter(%q) = %v, %v", expr, f, err)
		}
	}
}

func TestMatchesCaseInsensitively(t *testing.T) {
	f := filter(t, "level=error")
	if !f.Matches(record(t, `{"level":"ERROR"}`)) {
		t.Error("ERROR should match")
	}
	if f.Matches(record(t, `{"level":"info"}`)) {
		t.Error("info should not match")
	}
	if f.Matches(record(t, `{"msg":"no level"}`)) {
		t.Error("missing field should not match")
	}
	if f.Matches(nil) {
		t.Error("plain text should not match")
	}
}

func TestTermsAreANDed(t *testing.T) {
	f := filter(t, "level=error app=api")
	if !f.Matches(record(t, `{"level":"error","app":"api"}`)) {
		t.Error("both terms hold, should match")
	}
	if f.Matches(record(t, `{"level":"error","app":"web"}`)) {
		t.Error("one term fails, should not match")
	}
}

func TestNegationExcludesMatchesAndMissingFieldsPass(t *testing.T) {
	f := filter(t, "level!=debug")
	if !f.Matches(record(t, `{"level":"info"}`)) {
		t.Error("info should pass")
	}
	if f.Matches(record(t, `{"level":"DEBUG"}`)) {
		t.Error("DEBUG should be excluded")
	}
	// A record without the field is "not debug", so it passes.
	if !f.Matches(record(t, `{"msg":"x"}`)) {
		t.Error("missing field should pass a negated term")
	}
}

func TestMatchesFlattenedPathsAndEmptyValues(t *testing.T) {
	if !filter(t, "http.status=500").Matches(record(t, `{"http":{"status":500}}`)) {
		t.Error("flattened path should match")
	}
	if !filter(t, "note=").Matches(record(t, `{"note":""}`)) {
		t.Error("empty value should match")
	}
}

func TestRejectsMalformedTerms(t *testing.T) {
	for _, expr := range []string{"plainword", "=value", "!=value", "level=error oops"} {
		if _, err := ParseFilter(expr); err == nil {
			t.Errorf("should reject %q", expr)
		}
	}
}

func TestKeepsTheSourceExpression(t *testing.T) {
	if got := filter(t, " level=error ").Expr(); got != "level=error" {
		t.Errorf("expr %q", got)
	}
}

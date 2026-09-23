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

// Two values of one field are alternatives: a record cannot be both, and
// "errors or warnings" is the first thing picked from a list of levels.
// Negations still all hold.
func TestSameFieldTermsAreAlternatives(t *testing.T) {
	f := filter(t, "level=error level=warn app=api route!=/a route!=/b")
	for json, want := range map[string]bool{
		`{"level":"error","app":"api","route":"/c"}`: true,
		`{"level":"warn","app":"api","route":"/c"}`:  true,
		`{"level":"info","app":"api","route":"/c"}`:  false,
		`{"level":"error","app":"web","route":"/c"}`: false,
		`{"level":"error","app":"api","route":"/b"}`: false,
	} {
		if got := f.Matches(record(t, json)); got != want {
			t.Errorf("%s: matched %v, want %v", json, got, want)
		}
	}
}

// `level` names the level whichever well-known field carries it.
func TestLevelKeyFindsEveryLevelField(t *testing.T) {
	if !filter(t, "level=error").Matches(record(t, `{"severity":"ERROR"}`)) {
		t.Error("level=error did not find a severity field")
	}
}

// A value with spaces is quoted, and survives the round trip through Expr.
func TestQuotedValues(t *testing.T) {
	f := filter(t, `msg="connection reset" level=error`)
	if !f.Matches(record(t, `{"msg":"connection reset","level":"error"}`)) {
		t.Error("quoted value did not match")
	}
	again := filter(t, f.Expr())
	if again.Expr() != f.Expr() || !again.Has("msg", "connection reset") {
		t.Errorf("round trip gave %q from %q", again.Expr(), f.Expr())
	}
	if _, err := ParseFilter(`msg="open`); err == nil {
		t.Error("an unbalanced quote was accepted")
	}
}

// Toggling adds a term, toggling it again takes it out, and the last one
// taken out leaves no filter at all.
func TestToggle(t *testing.T) {
	var f *Filter
	f = f.Toggle("level", "error")
	f = f.Toggle("route", "/a b")
	if !f.Has("level", "error") || f.Expr() != `level=error route="/a b"` {
		t.Fatalf("toggled on: %q", f.Expr())
	}
	f = f.Toggle("level", "ERROR").Toggle("route", "/a b")
	if f != nil {
		t.Errorf("toggled off, left %q", f.Expr())
	}
}

// MatchesExcept sets one field's terms aside; with nothing left it matches
// everything, plain text included.
func TestMatchesExcept(t *testing.T) {
	f := filter(t, "level=error app=api")
	info := record(t, `{"level":"info","app":"api"}`)
	if f.Matches(info) || !f.MatchesExcept(info, LevelKey) {
		t.Error("the level term was not the only one set aside")
	}
	if f.MatchesExcept(record(t, `{"level":"info","app":"web"}`), LevelKey) {
		t.Error("the app term was set aside too")
	}
	var none *Filter
	if !none.MatchesExcept(nil, "") || !filter(t, "level=x").MatchesExcept(nil, LevelKey) {
		t.Error("an empty remainder did not match everything")
	}
}

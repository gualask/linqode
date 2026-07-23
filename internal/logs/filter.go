package logs

import (
	"fmt"
	"strings"
)

// Filter is a conjunction of field conditions applied to structured
// records, parsed from expressions like `level=error http.status!=200`.
// Lines that are not JSONL never match.
type Filter struct {
	terms []term
	expr  string
}

type term struct {
	key     string
	value   string
	negated bool
}

// ParseFilter parses a whitespace-separated list of `key=value` /
// `key!=value` terms. Values compare ASCII-case-insensitively. A nil
// filter with nil error means the expression was empty (no filter).
func ParseFilter(expr string) (*Filter, error) {
	var terms []term
	for _, word := range strings.Fields(expr) {
		key, value, negated := "", "", false
		if k, v, found := strings.Cut(word, "!="); found {
			key, value, negated = k, v, true
		} else if k, v, found := strings.Cut(word, "="); found {
			key, value = k, v
		} else {
			return nil, fmt.Errorf("`%s`: expected key=value or key!=value", word)
		}
		if key == "" {
			return nil, fmt.Errorf("`%s`: missing field name", word)
		}
		terms = append(terms, term{key: key, value: value, negated: negated})
	}
	if len(terms) == 0 {
		return nil, nil
	}
	return &Filter{terms: terms, expr: strings.TrimSpace(expr)}, nil
}

// Expr is the expression this filter was parsed from, kept verbatim for
// display and re-editing.
func (f *Filter) Expr() string {
	return f.expr
}

// Matches is true when every term holds for the record; plain-text lines
// (nil record) never match.
func (f *Filter) Matches(record *Record) bool {
	if record == nil {
		return false
	}
	for _, t := range f.terms {
		value, present := record.Get(t.key)
		holds := present && eqASCIIFold(value, t.value)
		if holds == t.negated {
			return false
		}
	}
	return true
}

// eqASCIIFold compares two strings for equality, folding ASCII letter case
// only.
func eqASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

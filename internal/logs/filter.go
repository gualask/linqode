package logs

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// LevelKey is the key a filter names a line's level by, whichever of the
// well-known level fields the record actually carries: picking `error` in
// the stats panel has to find the records that spell it `severity`.
const LevelKey = "level"

// Filter is a set of field conditions applied to structured records, parsed
// from expressions like `level=error level=warn http.status!=200`.
//
// Positive terms on the same field are alternatives and everything else must
// hold: the expression above is "an error or a warning, and not a 200". Two
// values of one field can never both be true of a record, so reading them as
// AND would only ever match nothing — and "errors or warnings" is the first
// thing anyone picks from a list of levels. Lines that are not JSONL never
// match.
type Filter struct {
	terms []term
}

type term struct {
	key     string
	value   string
	negated bool
}

// ParseFilter parses a whitespace-separated list of `key=value` /
// `key!=value` terms. A value holding spaces is written in double quotes
// (`msg="connection reset"`), and so is a key that could not be read bare
// (`"user agent"=curl`). Values compare ASCII-case-insensitively. A nil
// filter with nil error means the expression was empty (no filter).
func ParseFilter(expr string) (*Filter, error) {
	words, err := splitWords(expr)
	if err != nil {
		return nil, err
	}
	var terms []term
	for _, word := range words {
		key, value, negated, err := splitTerm(word)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, fmt.Errorf("`%s`: missing field name", word)
		}
		if strings.HasPrefix(value, `"`) {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("`%s`: unbalanced quotes", word)
			}
			value = unquoted
		}
		terms = append(terms, term{key: key, value: value, negated: negated})
	}
	if len(terms) == 0 {
		return nil, nil
	}
	return &Filter{terms: terms}, nil
}

// splitTerm cuts one word into its key, its value and whether it is negated.
// A quoted key is read to its closing quote, so it may hold what a bare key
// cannot: a space, an `=`, a trailing `!`.
func splitTerm(word string) (key, value string, negated bool, err error) {
	rest := word
	if strings.HasPrefix(word, `"`) {
		quoted, err := strconv.QuotedPrefix(word)
		if err != nil {
			return "", "", false, fmt.Errorf("`%s`: unbalanced quotes", word)
		}
		key, _ = strconv.Unquote(quoted)
		rest = word[len(quoted):]
		switch {
		case strings.HasPrefix(rest, "!="):
			return key, rest[2:], true, nil
		case strings.HasPrefix(rest, "="):
			return key, rest[1:], false, nil
		}
		return "", "", false, fmt.Errorf("`%s`: expected key=value or key!=value", word)
	}
	equals := strings.IndexByte(rest, '=')
	if equals < 0 {
		return "", "", false, fmt.Errorf("`%s`: expected key=value or key!=value", word)
	}
	key, value = rest[:equals], rest[equals+1:]
	if strings.HasSuffix(key, "!") {
		key, negated = key[:len(key)-1], true
	}
	return key, value, negated, nil
}

// splitWords splits at whitespace outside double quotes.
func splitWords(expr string) ([]string, error) {
	var words []string
	var word strings.Builder
	quoted, escaped := false, false
	for _, r := range expr {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case !quoted && unicode.IsSpace(r):
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
			continue
		}
		word.WriteRune(r)
	}
	if quoted {
		return nil, fmt.Errorf("`%s`: unbalanced quotes", word.String())
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words, nil
}

// Expr writes the filter back as an expression ParseFilter reads, for
// display and re-editing. It is rebuilt from the terms rather than kept,
// because a filter built by picking rows in the stats panel was never typed —
// and a field picked there can be named anything a JSON key can, so the key
// is quoted as well as the value when bare it would read as something else.
func (f *Filter) Expr() string {
	if f == nil {
		return ""
	}
	words := make([]string, len(f.terms))
	for index, t := range f.terms {
		operator := "="
		if t.negated {
			operator = "!="
		}
		key := t.key
		if strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || r == '"' || r == '=' }) ||
			strings.HasSuffix(key, "!") {
			key = strconv.Quote(key)
		}
		value := t.value
		if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '"' }) {
			value = strconv.Quote(value)
		}
		words[index] = key + operator + value
	}
	return strings.Join(words, " ")
}

// Has says whether the filter asks for key=value.
func (f *Filter) Has(key, value string) bool {
	if f == nil {
		return false
	}
	for _, t := range f.terms {
		if !t.negated && t.key == key && eqASCIIFold(t.value, value) {
			return true
		}
	}
	return false
}

// Toggle is the filter with key=value added, or taken out when it is already
// there; nil when nothing is left. The receiver is not changed.
//
// A value the filter excludes is replaced rather than joined. The stats panel
// lists it all the same — it counts a field with that field's own terms set
// aside — and picking it there means "these", which `key!=value
// key=value` could never show.
func (f *Filter) Toggle(key, value string) *Filter {
	var terms []term
	removed := false
	if f != nil {
		for _, t := range f.terms {
			if t.key == key && eqASCIIFold(t.value, value) {
				removed = removed || !t.negated
				continue
			}
			terms = append(terms, t)
		}
	}
	if !removed {
		terms = append(terms, term{key: key, value: value})
	}
	if len(terms) == 0 {
		return nil
	}
	return &Filter{terms: terms}
}

// Matches is true when the record satisfies the filter; plain-text lines
// (nil record) never match.
func (f *Filter) Matches(record *Record) bool {
	if record == nil {
		return false
	}
	return f.MatchesExcept(record, "")
}

// MatchesExcept is Matches with the terms on one key left out. It is how the
// stats panel counts a field while a filter is set on it: a list of levels
// counted under `level=error` would hold only errors, and the level you
// meant to add next would have vanished from the list you add it from. A
// nil filter, or one with nothing left once the key is set aside, matches
// everything, plain text included.
func (f *Filter) MatchesExcept(record *Record, except string) bool {
	if f == nil {
		return true
	}
	// Per key: whether it has positive terms, and whether one of them held.
	wanted := make(map[string]bool)
	for _, t := range f.terms {
		if t.key == except {
			continue
		}
		if record == nil {
			return false
		}
		value, present := lookup(record, t.key)
		holds := present && eqASCIIFold(value, t.value)
		if t.negated {
			if holds {
				return false
			}
			continue
		}
		wanted[t.key] = wanted[t.key] || holds
	}
	for _, held := range wanted {
		if !held {
			return false
		}
	}
	return true
}

// lookup is Record.Get, with LevelKey standing for whichever level field
// the record has.
func lookup(record *Record, key string) (string, bool) {
	if value, ok := record.Get(key); ok {
		return value, true
	}
	if key == LevelKey {
		return record.Level()
	}
	return "", false
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

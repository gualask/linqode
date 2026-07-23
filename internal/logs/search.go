package logs

import "strings"

// FindASCIICI returns the byte offset of the first occurrence of needle in
// haystack, comparing ASCII letters case-insensitively, or -1. Only ASCII
// case is folded, so the offset always falls on a UTF-8 rune boundary.
func FindASCIICI(haystack, needle string) int {
	return strings.Index(lowerASCII(haystack), lowerASCII(needle))
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Package logs is the log engine: line assembly from byte chunks, the
// bounded tail buffer, and search. It consumes generic streams of bytes and
// lines, so it works the same for docker compose logs, plain files, or any
// other remote command output.
package logs

import "strings"

// LineAssembler reassembles complete lines from a stream of byte chunks
// whose boundaries fall anywhere, keeping the trailing partial line across
// calls. Bytes are buffered until a newline, so a UTF-8 rune split across
// chunks rejoins before any string conversion.
type LineAssembler struct {
	partial []byte
}

// Push feeds one chunk and returns the lines it completed, in order. Line
// endings (\n, \r\n) are stripped; invalid UTF-8 is replaced with U+FFFD.
func (a *LineAssembler) Push(chunk []byte) []string {
	var lines []string
	for _, b := range chunk {
		if b == '\n' {
			lines = append(lines, a.take())
		} else {
			a.partial = append(a.partial, b)
		}
	}
	return lines
}

// Finish returns the unterminated final line, if the stream ended without a
// newline.
func (a *LineAssembler) Finish() (string, bool) {
	if len(a.partial) == 0 {
		return "", false
	}
	return a.take(), true
}

func (a *LineAssembler) take() string {
	line := a.partial
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	a.partial = a.partial[:0]
	return strings.ToValidUTF8(string(line), "�")
}

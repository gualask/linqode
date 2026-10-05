// Package logs is the log engine: line assembly from byte chunks, the
// bounded tail buffer, and search. It consumes generic streams of bytes and
// lines, so it works the same for docker compose logs, plain files, or any
// other remote command output.
package logs

import (
	"strings"
	"unicode/utf8"
)

// MaxLineBytes bounds one line. A line is held until its newline arrives, and
// nothing guarantees one does: a binary dumped to stdout, a progress bar
// redrawn with `\r` for an hour, a minified bundle logged whole. Unbounded,
// any of them grows the partial line for as long as the stream runs. Sixty-four
// KiB is far past any line worth reading on a terminal, and the tail buffer
// already holds thousands of lines, each of which may now be this long.
const MaxLineBytes = 64 << 10

// TruncatedSuffix ends a line that reached MaxLineBytes, so what is on screen
// says it is not the whole line.
const TruncatedSuffix = " …[truncated]"

// LineAssembler reassembles complete lines from a stream of byte chunks
// whose boundaries fall anywhere, keeping the trailing partial line across
// calls. Bytes are buffered until a newline, so a UTF-8 rune split across
// chunks rejoins before any string conversion.
//
// A line that reaches MaxLineBytes is emitted there, cut at a rune boundary
// and marked with TruncatedSuffix, and the rest of it up to the next newline
// is discarded rather than buffered.
type LineAssembler struct {
	partial []byte
	// discarding is set between a truncated line and its newline.
	discarding bool
}

// Push feeds one chunk and returns the lines it completed, in order. Line
// endings (\n, \r\n) are stripped; invalid UTF-8 is replaced with U+FFFD.
func (a *LineAssembler) Push(chunk []byte) []string {
	var lines []string
	for len(chunk) > 0 {
		end := len(chunk)
		if i := indexNewline(chunk); i >= 0 {
			end = i
		}
		if !a.discarding {
			// A line of exactly MaxLineBytes is whole; one more byte
			// before its newline is what makes it too long.
			if room := MaxLineBytes - len(a.partial); end <= room {
				a.partial = append(a.partial, chunk[:end]...)
			} else {
				a.partial = append(a.partial, chunk[:room]...)
				lines = append(lines, a.truncate())
				a.discarding = true
			}
		}
		if end == len(chunk) {
			break
		}
		if a.discarding {
			a.discarding = false
		} else {
			lines = append(lines, a.take())
		}
		chunk = chunk[end+1:]
	}
	return lines
}

// Finish returns the unterminated final line, if the stream ended without a
// newline.
func (a *LineAssembler) Finish() (string, bool) {
	a.discarding = false
	if len(a.partial) == 0 {
		return "", false
	}
	return a.take(), true
}

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

func (a *LineAssembler) take() string {
	line := a.partial
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	a.partial = a.partial[:0]
	return strings.ToValidUTF8(string(line), "�")
}

// truncate emits the full partial line with the marker, cut back to the last
// whole rune so the cut does not itself become a replacement character.
func (a *LineAssembler) truncate() string {
	line := a.partial
	for cut := len(line); cut > 0 && cut > len(line)-utf8.UTFMax; cut-- {
		if utf8.RuneStart(line[cut-1]) {
			if !utf8.FullRune(line[cut-1:]) {
				line = line[:cut-1]
			}
			break
		}
	}
	a.partial = a.partial[:0]
	return strings.ToValidUTF8(string(line), "�") + TruncatedSuffix
}

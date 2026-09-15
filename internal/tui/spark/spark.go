// Package spark draws readings as shapes made of block characters: a strip
// of one cell per sample, a bar of fractional width, and a histogram a few
// rows tall.
//
// There is one of these because there were two. The system view scaled its
// strips against their own window and coloured every cell by its reading;
// the live panel scaled against the peak and painted everything cyan. The
// rule the interface documents — height says how it moved, colour says how
// bad — held in one of them, and a third chart would have been a third
// variant. The shapes and the scales live here; what a reading means, and
// therefore its colour, stays with the caller that knows.
package spark

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// cells are the eight heights a single cell can draw, lowest first.
var cells = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// widths are the eight widths a single cell can draw, narrowest first, for a
// bar that grows sideways.
var widths = []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}

// Glyphs is every character a strip or a histogram is drawn with, for a test
// that has to find one in a rendered line.
const Glyphs = "▁▂▃▄▅▆▇█"

// Cell draws a fraction of one as one cell. Anything at or below zero is the
// lowest cell rather than a blank: a strip is a line of samples, and a
// sample of nothing is still a sample.
func Cell(fraction float64) string {
	index := int(fraction * float64(len(cells)))
	return cells[min(max(index, 0), len(cells)-1)]
}

// Strip draws one value per cell between floor and ceiling. The height is
// the shape and the colour is the caller's: style is asked about each cell
// by its index, so a strip can be coloured by its raw reading, by something
// the reading does not carry, or all in one colour.
func Strip(values []float64, floor, ceiling float64, style func(index int) lipgloss.Style) string {
	span := ceiling - floor
	if span <= 0 {
		span = 1
	}
	var strip strings.Builder
	for index, value := range values {
		strip.WriteString(style(index).Render(Cell((value - floor) / span)))
	}
	return strip.String()
}

// Bar draws a fraction of one as a bar width cells wide, padded to that width
// so that whatever follows it lines up. A fraction above zero is never drawn
// as nothing: the one error among ten thousand lines is the one worth seeing.
func Bar(fraction float64, width int) string {
	if width <= 0 {
		return ""
	}
	fraction = min(max(fraction, 0), 1)
	eighths := int(fraction*float64(width*len(widths)) + 0.5)
	if fraction > 0 {
		eighths = max(eighths, 1)
	}
	full, part := eighths/len(widths), eighths%len(widths)
	bar := strings.Repeat(widths[len(widths)-1], full)
	if part > 0 {
		bar += widths[part-1]
	}
	return bar + strings.Repeat(" ", width-full-min(part, 1))
}

// Columns draws one value per column as a histogram rows cells tall, top row
// first, every column measured against ceiling. Each column has rows × 8
// heights to be drawn at, and any value above zero is drawn at least one of
// them tall, for the same reason Bar never draws a small share as nothing.
// An empty column is blank rather than a floor of lowest cells: under a
// histogram, a floor would read as a little of something.
func Columns(values []float64, ceiling float64, rows int, style func(index int) lipgloss.Style) []string {
	if rows <= 0 {
		return nil
	}
	heights := make([]int, len(values))
	steps := rows * len(cells)
	for index, value := range values {
		if value <= 0 || ceiling <= 0 {
			continue
		}
		heights[index] = max(int(value/ceiling*float64(steps)+0.5), 1)
		heights[index] = min(heights[index], steps)
	}
	lines := make([]string, rows)
	for row := range rows {
		// The eighths already filled by the rows beneath this one.
		below := (rows - 1 - row) * len(cells)
		var line strings.Builder
		for index, height := range heights {
			eighths := min(max(height-below, 0), len(cells))
			if eighths == 0 {
				line.WriteByte(' ')
				continue
			}
			line.WriteString(style(index).Render(cells[eighths-1]))
		}
		lines[row] = line.String()
	}
	return lines
}

// MinimumSpan is how narrow a percentage window may get before it is widened
// around its own middle. Without it a reading that never moves more than a
// point would be drawn as if it had swung from empty to full — the failure
// mode on the other side of a fixed scale.
const MinimumSpan = 10

// Window is the range a strip is drawn against: its own extremes, widened to
// span around their middle and never below zero.
//
// Its own extremes, not a fixed scale, because the meter or the number
// beside a strip already says the level: memory between 78% and 88% drawn
// against 0–100 is eight solid blocks, and the climb inside it — the whole
// reason the strip is there — disappears.
func Window(values []float64, span float64) (float64, float64) {
	low, high := extremes(values)
	if gap := span - (high - low); gap > 0 {
		low, high = low-gap/2, high+gap/2
	}
	return max(low, 0), high
}

// Percent is Window for a share of something finite: widened to
// MinimumSpan, and clamped to what a percentage can be. A container's CPU is
// not one of these — two busy cores read as 200% — and is drawn against a
// plain Window instead.
func Percent(values []float64) (float64, float64) {
	low, high := Window(values, MinimumSpan)
	return low, min(high, 100)
}

// Relative is Window for an amount with no natural full, such as a
// container's memory in bytes: the narrowest window allowed is a share of
// the largest value rather than a fixed number, so four hundred megabytes
// moving by one is drawn flat and moving by sixty is drawn as a climb.
func Relative(values []float64, share float64) (float64, float64) {
	_, high := extremes(values)
	return Window(values, high*share)
}

// Rate is the range a throughput is drawn against. A rate has no natural
// full, so it is anchored at nothing and scaled to the busiest moment in the
// window: the strip says when the traffic happened, and the text beside it
// says how much.
func Rate(values []float64) (float64, float64) {
	_, high := extremes(values)
	return 0, high
}

func extremes(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	low, high := values[0], values[0]
	for _, value := range values {
		low, high = min(low, value), max(high, value)
	}
	return low, high
}

// Span says how much time a strip covers. It is measured from the samples
// that arrived rather than from the cadence that was asked for, so on a link
// slow enough to stretch the interval it reports what it actually got.
func Span(seconds float64) string {
	switch {
	case seconds >= 3600:
		return fmt.Sprintf("%.0fh", seconds/3600)
	case seconds >= 60:
		return fmt.Sprintf("%.0fm", seconds/60)
	default:
		return fmt.Sprintf("%.0fs", seconds)
	}
}

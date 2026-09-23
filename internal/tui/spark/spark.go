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
//
// That goes for the track as well: every shape here takes the fill it stands
// in as its last argument rather than reaching for the palette, so this
// package knows no colours at all and each caller says on the spot which
// background its shape is standing on. Every one of them passes theme.Track
// but the live panel's strip, and that exception is worth being able to see
// at the call site.
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
//
// track is drawn behind the cells, for a strip standing in a row of gauges
// where every other shape has one; pass an empty style for a strip on the
// terminal's own background.
func Strip(values []float64, floor, ceiling float64,
	style func(index int) lipgloss.Style, track lipgloss.Style) string {
	span := ceiling - floor
	if span <= 0 {
		span = 1
	}
	var strip strings.Builder
	for index, value := range values {
		strip.WriteString(style(index).Inherit(track).Render(Cell((value - floor) / span)))
	}
	return strip.String()
}

// Bar draws a fraction of one as a bar width cells wide, filled from the
// left and standing in track for the rest of them, so the shape is there
// before the reading is and what is not filled says how much was not.
//
// The last filled cell is a fraction of a cell where the reading falls
// between two — the eighths are the horizontal twin of the half-cell that
// tops a column in Bars — which is what lets a four-cell bar say more than
// four things. A fraction above zero is never drawn as nothing: the one
// error among ten thousand lines is the one worth seeing.
//
// Only the bar takes the reading's style; the track keeps its own. Rendering
// the whole width in the reading's colour turned the empty part into a field
// of bright speckle, where the part that means "unused" shouted as loudly as
// the part that means "used".
func Bar(fraction float64, width int, style, track lipgloss.Style) string {
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
		// The part-filled cell is the reading over the track: its left is the
		// bar's colour and the rest of it is what the bar has not reached.
		bar += widths[part-1]
	}
	return style.Inherit(track).Render(bar) +
		track.Render(strings.Repeat(" ", width-full-min(part, 1)))
}

// Meter is Bar for a percentage, which is how every gauge on the screen asks
// for one: clamped at both ends, so a reading past a hundred fills the width
// rather than overflowing it.
func Meter(percent float64, width int, style, track lipgloss.Style) string {
	return Bar(percent/100, width, style, track)
}

// Segments draws several shares as one bar width cells wide: a run of cells
// per value, in the order given, each in its own style. It is the shape for
// "how much of this is that" — a hundred lines of which three are errors is
// three cells of red against ninety-seven of green, and a log in trouble is
// a bar that has gone red.
//
// A share above zero is never drawn as nothing, for the reason Bar does not
// draw one either: the segment that matters is usually the small one. The
// cell it takes is borrowed from the widest segment, which can spare it, so
// the bar is exactly width cells whatever it holds. The rest is largest
// remainders, so the rounding does not all fall on the same share.
//
// Everything at or below zero takes no cells at all: a level with nothing in
// it is absent from the bar rather than a mark that means nothing.
func Segments(values []float64, width int,
	style func(index int) lipgloss.Style, track lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	total := 0.0
	for _, value := range values {
		total += max(value, 0)
	}
	if total <= 0 {
		return track.Render(strings.Repeat(" ", width))
	}

	exact := make([]float64, len(values))
	cells := make([]int, len(values))
	used := 0
	for index, value := range values {
		if value <= 0 {
			continue
		}
		exact[index] = value / total * float64(width)
		cells[index] = max(int(exact[index]), 1)
		used += cells[index]
	}
	for used < width {
		cells[widest(exact, cells, 1)]++
		used++
	}
	for used > width {
		index := widest(nil, cells, 2)
		if index < 0 {
			// More shares than there are cells. Something has to go, and it
			// is the smallest: a bar of one-cell segments says every level
			// is equally big, which is the one thing it must never say.
			index = leastEarned(exact, cells)
		}
		cells[index]--
		used--
	}

	var bar strings.Builder
	for index, count := range cells {
		if count > 0 {
			bar.WriteString(style(index).Inherit(track).Render(strings.Repeat("█", count)))
		}
	}
	return bar.String()
}

// widest is the segment a cell is given to or taken from: the one with the
// largest share still unpaid when exact is given, the widest one otherwise.
// floor is the size a segment may not be taken below, which is what keeps a
// share above zero from being rounded away; -1 when nothing qualifies.
func widest(exact []float64, cells []int, floor int) int {
	pick, best := -1, 0.0
	for index, count := range cells {
		if count < floor {
			continue
		}
		score := float64(count)
		if exact != nil {
			score = exact[index] - float64(count)
		}
		if pick < 0 || score > best {
			pick, best = index, score
		}
	}
	return pick
}

// leastEarned is the drawn segment with the smallest share behind it, which
// is the one to drop when they cannot all be drawn.
func leastEarned(exact []float64, cells []int) int {
	pick, best := -1, 0.0
	for index, count := range cells {
		if count <= 0 {
			continue
		}
		if pick < 0 || exact[index] < best {
			pick, best = index, exact[index]
		}
	}
	return pick
}

// Columns draws one value per column as a histogram rows cells tall, top row
// first, every column measured against ceiling. Each column has rows × 8
// heights to be drawn at, and any value above zero is drawn at least one of
// them tall, for the same reason Bar never draws a small share as nothing.
// An empty column is the bare track rather than a floor of lowest cells:
// under a histogram, a floor would read as a little of something.
func Columns(values []float64, ceiling float64, rows int,
	style func(index int) lipgloss.Style, track lipgloss.Style) []string {
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
				line.WriteString(track.Render(" "))
				continue
			}
			line.WriteString(style(index).Inherit(track).Render(cells[eighths-1]))
		}
		lines[row] = line.String()
	}
	return lines
}

// bars are the three states of a cell in a solid column: empty, filled to the
// middle, filled to the top.
var bars = []string{" ", "▄", "█"}

// Bars draws one value per column, rows tall, measured against ceiling. A
// column is solid up to its top cell, which is a half where the value falls
// between two rows, so a column drawn in rows rows has rows × 2 heights.
//
// It is the shape for a history that has to survive being four rows tall, and
// it is the meter's shape stood on end: the columns touch, and what is not
// filled is the track behind them. Columns is the finer drawing — eight
// heights a row — and what it draws in four rows is a ragged edge of eighths.
//
// Any value above zero is drawn at least a half cell tall, for the reason Bar
// never draws a small share as nothing. track is drawn behind every column,
// including the ones with nothing in them, so a chart has its shape before it
// has its history; pass an empty style for columns standing on the terminal's
// own background.
func Bars(values []float64, ceiling float64, rows int,
	style func(index int) lipgloss.Style, track lipgloss.Style) []string {
	if rows <= 0 {
		return nil
	}
	steps := rows * 2
	heights := make([]int, len(values))
	for index, value := range values {
		if value <= 0 || ceiling <= 0 {
			continue
		}
		heights[index] = min(max(int(value/ceiling*float64(steps)+0.5), 1), steps)
	}
	lines := make([]string, rows)
	for row := range rows {
		// The halves already filled by the rows beneath this one.
		below := (rows - 1 - row) * 2
		var line strings.Builder
		for index, height := range heights {
			filled := min(max(height-below, 0), len(bars)-1)
			if filled == 0 {
				line.WriteString(track.Render(" "))
				continue
			}
			// The top cell of a column is half reading and half track, so it
			// takes the reading's colour over the track's fill.
			line.WriteString(style(index).Inherit(track).Render(bars[filled]))
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

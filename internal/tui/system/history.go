package system

// The trend behind each reading, kept client-side.
//
// This costs the server nothing: the samples have already been fetched and
// paid for, and keeping the last few is the difference between "memory is at
// 88%" and "memory has been climbing for ten minutes", which is a different
// sentence about the same machine. It is the one part of the plan that is
// free by construction, and the reason the sampler's cadence is worth
// anything — a reading nobody remembers is a reading that can only ever say
// what is true right now.
//
// Nothing here survives the session. Retention while nobody is connected is
// what an agent would buy, and it is a non-goal (docs/PROJECT.md).

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// trend is one remembered sample: the readings worth a sparkline, and the
// host's own clock so the window can say how long it covers.
type trend struct {
	uptime           float64
	cpu, memory, net float64
}

// historyDepth is how many samples are kept. At the five-second cadence that
// is ten minutes, which is more than any sparkline draws — the extra is
// there so a stretched interval on a slow link still fills the strip.
const historyDepth = 120

// sparkWidth is how many of them a strip shows. Twenty-four cells is two
// minutes at the usual cadence: long enough to show a climb, short enough
// that the right-hand end still means "just now".
const sparkWidth = 24

// history is a ring of the last historyDepth samples, oldest first when
// read back.
type history struct {
	samples []trend
}

func (h *history) push(sample trend) {
	h.samples = append(h.samples, sample)
	if len(h.samples) > historyDepth {
		h.samples = h.samples[len(h.samples)-historyDepth:]
	}
}

// window is the last n samples, oldest first, and how many seconds of host
// time they cover. Fewer than two of them is not a trend.
func (h *history) window(n int) ([]trend, float64) {
	samples := h.samples
	if len(samples) > n {
		samples = samples[len(samples)-n:]
	}
	if len(samples) < 2 {
		return nil, 0
	}
	return samples, samples[len(samples)-1].uptime - samples[0].uptime
}

// sparkline draws one value per cell between a floor and a ceiling. The
// height is the shape and the color is the value: a cell is colored by the
// style the caller gives its raw reading, so a memory strip stays yellow
// while it climbs even though its cells are drawn against a window ten
// points wide.
//
// That split is the whole reason the strip is worth a place beside a meter.
// The meter already says how full the resource is; if the strip said the
// same thing it would be a second bar, which is exactly what a fixed 0–100
// scale turns it into — memory sitting between 78% and 88% draws as eight
// solid blocks and the climb inside it disappears.
func sparkline(values []float64, floor, ceiling float64, style func(float64) lipgloss.Style) string {
	span := ceiling - floor
	if span <= 0 {
		span = 1
	}
	var strip strings.Builder
	for _, value := range values {
		strip.WriteString(style(value).Render(sparkCell((value - floor) / span * 100)))
	}
	return strip.String()
}

// minimumSpan is how narrow a percentage window may get before it is widened
// around its own middle. Without it a reading that never moves more than a
// point would be drawn as if it had swung from empty to full — the failure
// mode on the other side of the fixed scale.
const minimumSpan = 10

// percentScale is the window a percentage strip is drawn against: its own
// extremes, widened to minimumSpan and clamped to what a percentage can be.
func percentScale(values []float64) (float64, float64) {
	low, high := extremes(values)
	if gap := minimumSpan - (high - low); gap > 0 {
		low, high = low-gap/2, high+gap/2
	}
	return max(low, 0), min(high, 100)
}

// rateScale is the window a throughput strip is drawn against. A rate has no
// natural full, so it is anchored at nothing and scaled to the busiest
// moment in the window: the strip says when the traffic happened, and the
// text beside it says how much.
func rateScale(values []float64) (float64, float64) {
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

// sparkSpan says how much host time a strip covers. The window is however
// many samples arrived, not however many were asked for, so on a link slow
// enough to stretch the interval this reports what it actually got rather
// than the cadence it wanted.
func sparkSpan(seconds float64) string {
	switch {
	case seconds >= 3600:
		return fmt.Sprintf("%.0fh", seconds/3600)
	case seconds >= 60:
		return fmt.Sprintf("%.0fm", seconds/60)
	default:
		return fmt.Sprintf("%.0fs", seconds)
	}
}

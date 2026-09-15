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
// what an agent would buy, and it is a non-goal (docs/PROJECT.md). How a
// strip is drawn and scaled is internal/tui/spark.

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

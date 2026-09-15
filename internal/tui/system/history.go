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

import "time"

// trend is one remembered sample: the readings worth a sparkline, and the
// host's own clock so the window can say how long it covers.
type trend struct {
	uptime           float64
	cpu, memory, net float64
	// disks is each filesystem's used kilobytes, by mount point.
	disks map[string]uint64
}

// historyDepth is how many samples are kept. At the five-second cadence that
// is ten minutes, which is more than any sparkline draws — the extra is
// there so a stretched interval on a slow link still fills the strip.
const historyDepth = 120

// stripMinimum is the narrowest a strip column is drawn. Twenty-four cells is
// two minutes at the usual cadence, the least that shows a climb; a terminal
// with less room than that gives the column up. One with more widens it, up
// to everything the history holds.
const stripMinimum = 24

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

// A filesystem's fill time is only projected from evidence that can carry it.
const (
	// fillSamples and fillWindow are the least history a projection stands
	// on: six samples and a minute of host time.
	fillSamples = 6
	fillWindow  = 60.0
	// fillGrowthKB is the least a filesystem must have grown across that
	// window, so the kilobyte resolution of df is not read as a trend.
	fillGrowthKB = 1024.0
	// fillReach and fillHorizon bound how far a projection may reach: a
	// hundred times the window it was measured over, and never past a day.
	// Ten minutes of growth say something about the next few hours and
	// nothing about next week, when a log rotation or a cleanup job will
	// have happened.
	fillReach   = 100.0
	fillHorizon = 24 * time.Hour
)

// fillTime is how long a filesystem has before it is full at the rate its
// history says it is filling, and whether that is worth saying at all.
//
// The rate is a least-squares slope over every remembered sample rather than
// the difference between the first and the last, so a single write or delete
// at either end does not decide it. It is said only when the filesystem is
// growing, has grown by more than df can mis-round, and will be full within
// the reach of the evidence: a disk that fills in three days on the strength
// of ten minutes is a guess, and a meter already says how full it is.
func (h *history) fillTime(mount string, usedKB, totalKB uint64) (time.Duration, bool) {
	if usedKB >= totalKB {
		return 0, false
	}
	var xs, ys []float64
	for _, sample := range h.samples {
		if used, ok := sample.disks[mount]; ok {
			xs, ys = append(xs, sample.uptime), append(ys, float64(used))
		}
	}
	if len(xs) < fillSamples {
		return 0, false
	}
	// A reboot inside the history turns host time backwards, which leaves no
	// window worth the name.
	window := xs[len(xs)-1] - xs[0]
	if window < fillWindow {
		return 0, false
	}
	var meanX, meanY float64
	for index := range xs {
		meanX += xs[index]
		meanY += ys[index]
	}
	meanX /= float64(len(xs))
	meanY /= float64(len(ys))
	var covariance, variance float64
	for index := range xs {
		covariance += (xs[index] - meanX) * (ys[index] - meanY)
		variance += (xs[index] - meanX) * (xs[index] - meanX)
	}
	if variance == 0 {
		return 0, false
	}
	slope := covariance / variance // kilobytes a second
	if slope <= 0 || slope*window < fillGrowthKB {
		return 0, false
	}
	seconds := float64(totalKB-usedKB) / slope
	if seconds > min(fillReach*window, fillHorizon.Seconds()) {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

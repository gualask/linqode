package home

// The screen's sampling cadence, in one place.
//
// Before this, three timers lived in two packages: the table refreshed itself
// every five seconds, the container readings every twenty, the meters on a
// third clock of their own. Nothing could see the whole picture, so nothing
// could answer the questions that matter over SSH — is a read already in
// flight, is anyone looking at what it feeds, is this link slow enough that
// the interval is now a lie.
//
// One heartbeat drives everything. Each source carries its own interval and
// is asked, once a beat, whether it is due; a source nobody can see is never
// due at all. This is the shape the roadmap's phase B wants: one owner of the
// cadence, so a reading added later costs a line rather than a timer.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// beat is how often sources are asked whether they are due. It bounds how
// late a read can be, not how often one happens: at one second, an interval
// is honoured to within a second, and a tick that finds nothing due costs a
// map lookup rather than a round-trip.
const beat = time.Second

// inFlightBudget is how much of its own interval a read may occupy before the
// interval is stretched to keep it there. A quarter is the rule the cost
// budget already encodes for `docker stats` — "in flight a tenth of the time"
// at twenty seconds — generalised: on a link slow enough that a read takes
// longer than that, the honest response is to ask for it less often, not to
// queue up reads that overlap.
const inFlightBudget = 4

// sourceID names a thing worth reading off the remote host.
type sourceID int

const (
	sourceServices sourceID = iota
	sourceHost
	sourceStats
	sourceProcesses
	sourceDiskUsage
)

// source is one remote reading: how often it is wanted, how to start it, and
// whether anyone is looking at what it feeds.
type source struct {
	// every is the interval asked for. A source with no interval is only
	// ever read on demand — by `r`, or by something that invalidates it.
	every time.Duration
	// start begins the read. A nil start means the capability is not
	// configured, and the source is never due.
	start func() tea.Cmd
	// gate reports whether this reading is worth taking right now. A nil
	// gate always is.
	gate func() bool

	inFlight bool
	// started is when the last read began, so intervals are measured
	// start to start and a slow read does not shorten the next wait.
	started time.Time
	// stretched is the interval the last round-trip earned: a read that
	// took a second is not asked for again for four.
	stretched time.Duration
}

func (s *source) interval() time.Duration { return max(s.every, s.stretched) }

func (s *source) due(now time.Time) bool {
	switch {
	case s.start == nil, s.inFlight, s.every == 0:
		return false
	case s.gate != nil && !s.gate():
		return false
	case s.started.IsZero():
		return true
	default:
		return now.Sub(s.started) >= s.interval()
	}
}

type sampler struct {
	sources map[sourceID]*source
	// now is the clock, injectable so the cadence can be tested without
	// waiting for it.
	now func() time.Time
}

func newSampler(sources map[sourceID]*source) *sampler {
	return &sampler{sources: sources, now: time.Now}
}

func heartbeat() tea.Cmd {
	return tea.Tick(beat, func(time.Time) tea.Msg { return beatMsg{} })
}

type beatMsg struct{}

// due starts every source whose turn has come, in one batch.
func (s *sampler) due() tea.Cmd {
	now := s.now()
	var cmds []tea.Cmd
	for _, source := range s.sources {
		if !source.due(now) {
			continue
		}
		cmds = append(cmds, s.begin(source, now))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// read starts one source on demand, whatever its interval says — what `r`
// does, and what an event that invalidates a reading will do. It still
// refuses to overlap a read already in flight.
func (s *sampler) read(id sourceID) tea.Cmd {
	source, ok := s.sources[id]
	if !ok || source.start == nil || source.inFlight {
		return nil
	}
	return s.begin(source, s.now())
}

// begin starts a read, unless the source turns out to have nothing to read
// right now: the container counters are addressed by container, so they have
// nothing to ask about until the first service list has landed. A source that
// declines is left exactly as it was, so the next beat can try again — marking
// it in flight would strand it there forever, since no answer is coming back
// to say it finished.
func (s *sampler) begin(source *source, now time.Time) tea.Cmd {
	cmd := source.start()
	if cmd == nil {
		return nil
	}
	source.inFlight, source.started = true, now
	return cmd
}

// setInterval changes what a source asks for. It is how watching the daemon
// pushes the service list onto a slow safety net, and how losing the stream
// puts it back on a short one.
func (s *sampler) setInterval(id sourceID, every time.Duration) {
	if source, ok := s.sources[id]; ok {
		source.every = every
	}
}

// finished records that a read came back, and what it cost. The cost is kept
// as-is rather than averaged: a link that just got slow should be believed
// immediately, and one that got fast again shortens the wait on its next
// reading rather than several later.
func (s *sampler) finished(id sourceID) {
	source, ok := s.sources[id]
	if !ok || !source.inFlight {
		return
	}
	source.inFlight = false
	if elapsed := s.now().Sub(source.started); elapsed > 0 {
		source.stretched = elapsed * inFlightBudget
	}
}

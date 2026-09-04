package home

// Tests for the cadence. The clock is injected rather than waited on: what
// matters here is the arithmetic of when a read is allowed to start, and a
// test that slept for it would take twenty seconds to prove one branch.

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/status"
)

// clock is a hand-wound time source.
type clock struct{ at time.Time }

func (c *clock) now() time.Time       { return c.at }
func (c *clock) tick(d time.Duration) { c.at = c.at.Add(d) }

// counted is a source whose reads are counted instead of performed.
func counted(every time.Duration, calls *int) *source {
	return &source{every: every, start: func() tea.Cmd {
		*calls++
		return func() tea.Msg { return nil }
	}}
}

func newTestSampler(sources map[sourceID]*source) (*sampler, *clock) {
	c := &clock{at: time.Now()}
	s := newSampler(sources)
	s.now = c.now
	return s, c
}

func TestASourceIsDueOnceItsIntervalHasPassed(t *testing.T) {
	calls := 0
	sampler, clock := newTestSampler(map[sourceID]*source{
		sourceHost: counted(5*time.Second, &calls),
	})

	// The first beat reads: nothing has been read yet, and a dashboard that
	// waited a full interval before its first sample would open empty.
	sampler.due()
	if calls != 1 {
		t.Fatalf("%d reads on the first beat, want 1", calls)
	}
	sampler.finished(sourceHost)

	clock.tick(4 * time.Second)
	sampler.due()
	if calls != 1 {
		t.Errorf("%d reads after 4s of a 5s interval", calls)
	}

	clock.tick(2 * time.Second)
	sampler.due()
	if calls != 2 {
		t.Errorf("%d reads after the interval passed, want 2", calls)
	}
}

// Nothing is asked for twice at once: on a slow link the next beat comes
// round long before the answer does.
func TestASourceDoesNotOverlapItself(t *testing.T) {
	calls := 0
	sampler, clock := newTestSampler(map[sourceID]*source{
		sourceHost: counted(time.Second, &calls),
	})
	sampler.due()
	clock.tick(2 * time.Second)
	sampler.due()
	sampler.due()
	if calls != 1 {
		t.Fatalf("%d reads started while one was in flight", calls)
	}
	// The read took two seconds, so its interval is stretched to eight.
	sampler.finished(sourceHost)
	clock.tick(9 * time.Second)
	sampler.due()
	if calls != 2 {
		t.Errorf("%d reads after the first came back, want 2", calls)
	}
}

// A read that takes a second is not asked for again for four: a command in
// flight most of the time is a command whose interval is a fiction.
func TestASlowReadStretchesItsOwnInterval(t *testing.T) {
	calls := 0
	sampler, clock := newTestSampler(map[sourceID]*source{
		sourceStats: counted(time.Second, &calls),
	})
	sampler.due()
	clock.tick(2 * time.Second)
	sampler.finished(sourceStats)

	// The interval says one second, the last round-trip says eight.
	clock.tick(5 * time.Second)
	sampler.due()
	if calls != 1 {
		t.Errorf("%d reads at 5s after a 2s round-trip, want 1", calls)
	}
	clock.tick(4 * time.Second)
	sampler.due()
	if calls != 2 {
		t.Errorf("%d reads once the stretched interval passed, want 2", calls)
	}
}

// A reading nobody is looking at is not taken. The gate is what stops the
// screen paying two seconds for container readings the live stream is already
// delivering every second.
func TestAGatedSourceIsNeverDue(t *testing.T) {
	calls, open := 0, true
	gated := counted(time.Second, &calls)
	gated.gate = func() bool { return !open }
	sampler, clock := newTestSampler(map[sourceID]*source{sourceStats: gated})

	sampler.due()
	if calls != 0 {
		t.Fatalf("%d reads through a closed gate", calls)
	}
	open = false
	clock.tick(time.Second)
	sampler.due()
	if calls != 1 {
		t.Errorf("%d reads once the gate opened, want 1", calls)
	}
}

// A capability this host does not offer has no fetch, so it has no cadence
// either — nothing is scheduled for a reading that cannot be taken.
func TestAnUnconfiguredSourceIsNeverDue(t *testing.T) {
	sampler, _ := newTestSampler(map[sourceID]*source{
		sourceHost: {every: time.Second},
	})
	if cmd := sampler.due(); cmd != nil {
		t.Error("a source with no fetch was scheduled")
	}
	if cmd := sampler.read(sourceHost); cmd != nil {
		t.Error("a source with no fetch was read on demand")
	}
}

// `r` does not wait for a turn — but it still refuses to overlap.
func TestOnDemandReadIgnoresTheIntervalButNotTheFlight(t *testing.T) {
	calls := 0
	sampler, _ := newTestSampler(map[sourceID]*source{
		sourceServices: counted(time.Hour, &calls),
	})
	sampler.due()
	sampler.finished(sourceServices)
	if cmd := sampler.read(sourceServices); cmd == nil {
		t.Fatal("an on-demand read waited for the interval")
	}
	if cmd := sampler.read(sourceServices); cmd != nil {
		t.Error("an on-demand read started while one was in flight")
	}
}

// While another view is on screen the heartbeat stays armed — the cadence
// resumes on return — but nothing is read for a screen nobody is looking at.
func TestBackgroundKeepsTheHeartbeatWithoutReading(t *testing.T) {
	services, hosts := 0, 0
	screen := New(Config{
		Services: func() ([]compose.Service, error) { services++; return nil, nil },
		Host:     func() (host.Metrics, error) { hosts++; return host.Metrics{}, nil },
	}, status.New(status.Config{}))

	cmd, handled := screen.UpdateBackground(beatMsg{})
	if !handled || cmd == nil {
		t.Fatal("the heartbeat was not rescheduled in the background")
	}
	if services != 0 || hosts != 0 {
		t.Fatalf("a hidden screen read the host: %d service reads, %d host reads",
			services, hosts)
	}

	// A read already in flight when the view opened still lands.
	if _, handled := screen.UpdateBackground(
		hostSampleMsg{metrics: host.Metrics{CPUs: 4}}); !handled {
		t.Fatal("an in-flight sample was dropped in the background")
	}
	if !screen.system.HasBand() {
		t.Error("the in-flight sample was not applied")
	}
}

// The container readings stand down while the live stream is open, and take
// the columns back when it closes.
func TestContainerReadingsStandDownWhileTheStreamIsOpen(t *testing.T) {
	panel := status.New(status.Config{Stats: true, LiveStats: true})
	calls := 0
	screen := New(Config{
		Stats: func() ([]compose.ContainerStats, error) { calls++; return nil, nil },
	}, panel)

	applyScreen(screen, screen.sampler.due())
	if calls != 1 {
		t.Fatalf("%d readings taken on the first beat, want 1", calls)
	}
	// `a` asks the application for the stream; the panel counts itself live
	// from that moment, which is what closes the gate.
	panel.Update(key("a"))
	if !panel.LiveActive() {
		t.Fatal("the panel did not report the stream as starting")
	}
	if cmd := screen.sampler.due(); cmd != nil {
		t.Error("a reading was taken while the stream was feeding the columns")
	}
}

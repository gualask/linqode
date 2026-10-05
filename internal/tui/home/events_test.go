package home

// Tests for watching the daemon: that the stream is only opened once there is
// a project to scope it to, that a change re-reads the table, that news
// arriving mid-read is not lost, and that losing the stream puts the timer
// back where it was.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/status"
)

// watchStream is a hand-built feed standing in for the remote command.
type watchStream struct {
	events  chan operations.Event
	stopped bool
}

func newWatchStream() *watchStream {
	return &watchStream{events: make(chan operations.Event, 16)}
}

func (w *watchStream) feed() operations.Feed {
	return operations.Feed{Events: w.events, Stop: func() { w.stopped = true }}
}

func (w *watchStream) change(container string) {
	w.events <- operations.Event{Kind: operations.EventChange,
		Change: compose.Event{Action: "die", Container: container, ExitCode: "137"}}
}

// watched builds a screen whose service list names a project and whose watch
// hands back the given stream, and drives it to the point where the stream is
// open. reads counts the `ps` round-trips.
func watched(t *testing.T, stream *watchStream) (*Model, *int) {
	t.Helper()
	reads := 0
	screen := New(Config{
		Services: func() ([]compose.Service, error) {
			reads++
			return []compose.Service{{Service: "web", Name: "app-web-1",
				Project: "app", State: "running"}}, nil
		},
		Watch: func(project string) (operations.Feed, error) {
			if project != "app" {
				t.Errorf("watch scoped to %q, want the project from ps", project)
			}
			return stream.feed(), nil
		},
	}, status.New(status.Config{}))
	screen.SetSize(120, 30)
	applyScreen(screen, screen.sampler.due())
	if screen.watch == nil {
		t.Fatal("the stream was not opened after the first service list")
	}
	return screen, &reads
}

// Nothing is watched until `ps` has said what project these containers belong
// to: an unscoped stream would report every container on the host.
func TestWatchingWaitsForTheProjectName(t *testing.T) {
	opened := 0
	screen := New(Config{
		Services: func() ([]compose.Service, error) { return nil, nil },
		Watch: func(string) (operations.Feed, error) {
			opened++
			return newWatchStream().feed(), nil
		},
	}, status.New(status.Config{}))

	applyScreen(screen, screen.sampler.due())
	if opened != 0 {
		t.Fatalf("the stream was opened %d times with no project to scope it to", opened)
	}
}

// While the stream is up the timer steps back to a safety net; losing the
// stream puts it back on the short interval it had before.
func TestWatchingSlowsTheTimerAndLosingItRestoresIt(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)

	if got := screen.sampler.sources[sourceServices].every; got != servicesWatched {
		t.Errorf("interval while watching = %s, want %s", got, servicesWatched)
	}

	close(stream.events)
	screen.handleWatchTick()
	if screen.watch != nil {
		t.Error("the screen kept a stream that had ended")
	}
	if got := screen.sampler.sources[sourceServices].every; got != servicesRefresh {
		t.Errorf("interval after the stream ended = %s, want %s", got, servicesRefresh)
	}
}

// flakyDaemon is a host whose `ps` and `docker events` can be told to fail,
// on a hand-wound clock, counting what each was asked for.
type flakyDaemon struct {
	clock          *clock
	reads, opens   int
	psDown         bool
	streams        []*watchStream
	screen         *Model
	servicesFailed error
}

func newFlakyDaemon(t *testing.T) *flakyDaemon {
	t.Helper()
	d := &flakyDaemon{clock: &clock{at: time.Now()}, servicesFailed: errors.New("daemon down")}
	d.screen = New(Config{
		Services: func() ([]compose.Service, error) {
			d.reads++
			if d.psDown {
				return nil, d.servicesFailed
			}
			return []compose.Service{{Service: "web", Name: "app-web-1",
				Project: "app", State: "running"}}, nil
		},
		Watch: func(string) (operations.Feed, error) {
			d.opens++
			stream := newWatchStream()
			d.streams = append(d.streams, stream)
			return stream.feed(), nil
		},
	}, status.New(status.Config{}))
	d.screen.sampler.now = d.clock.now
	d.screen.SetSize(120, 30)
	applyScreen(d.screen, d.screen.sampler.due())
	if d.opens != 1 {
		t.Fatalf("the stream was opened %d times after the first list, want 1", d.opens)
	}
	return d
}

// end closes the stream now up and lets the screen notice, applying whatever
// that asks for.
func (d *flakyDaemon) end() {
	close(d.streams[len(d.streams)-1].events)
	applyScreen(d.screen, d.screen.handleWatchTick())
}

// beat winds the clock on and runs one heartbeat's worth of due reads.
func (d *flakyDaemon) beat(by time.Duration) {
	d.clock.tick(by)
	applyScreen(d.screen, d.screen.sampler.due())
}

// A stream that ends does not force a read: the timer it handed the list back
// to decides when the next one is. Forcing it, with the daemon down, was a
// `ps` and a fresh `docker events` every drain interval — four a second.
func TestAnEndedStreamLeavesTheNextReadToTheTimer(t *testing.T) {
	d := newFlakyDaemon(t)
	before := d.reads
	d.psDown = true
	d.end()
	if d.reads != before {
		t.Errorf("the stream's end forced %d reads, want none", d.reads-before)
	}
	// The interval is the short one again, measured from the last read.
	d.beat(servicesRefresh)
	if d.reads != before+1 {
		t.Errorf("%d reads one interval after the end, want 1", d.reads-before)
	}
}

// A `ps` that failed says nothing about whether the daemon is there to watch,
// and is no reason to open a stream: the project it would be scoped to is the
// one the last good read reported, and the daemon that just refused the read
// will refuse the stream too.
func TestAFailedReadDoesNotReopenTheStream(t *testing.T) {
	d := newFlakyDaemon(t)
	d.psDown = true
	d.end()
	for range 5 {
		d.beat(servicesRefresh)
	}
	if d.opens != 1 {
		t.Errorf("the stream was reopened %d times after failed reads", d.opens-1)
	}
	d.psDown = false
	d.beat(servicesRefresh)
	if d.opens != 2 || d.screen.watch == nil {
		t.Errorf("a good read did not reopen the stream (opens %d)", d.opens)
	}
}

// A stream that keeps ending as soon as it opens is retried less and less
// often, up to the safety net's minute, rather than reopened on every read.
// One that lived a while ended for a reason of its own and is reopened at
// once.
func TestAStreamThatKeepsEndingIsRetriedLessOften(t *testing.T) {
	d := newFlakyDaemon(t)
	waits := []time.Duration{}
	for range 6 {
		d.end()
		opens := d.opens
		waited := time.Duration(0)
		for d.opens == opens && waited < 5*time.Minute {
			d.beat(time.Second)
			waited += time.Second
		}
		waits = append(waits, waited)
	}
	for index := 1; index < len(waits); index++ {
		if waits[index] < waits[index-1] {
			t.Errorf("the retry shortened: %v", waits)
		}
	}
	if last := waits[len(waits)-1]; last < 50*time.Second || last > servicesWatched+servicesRefresh {
		t.Errorf("the retry settled at %v, want about the safety net's %v: %v",
			last, servicesWatched, waits)
	}

	// A stream that stayed up for the safety net's interval resets it.
	d.clock.tick(servicesWatched)
	d.end()
	opens := d.opens
	d.beat(servicesRefresh)
	if d.opens != opens+1 {
		t.Errorf("a stream that lived a minute was not reopened on the next read")
	}
}

// A change re-reads the table, without waiting for the next interval.
func TestAChangeReReadsTheTable(t *testing.T) {
	stream := newWatchStream()
	screen, reads := watched(t, stream)
	before := *reads

	stream.change("app-web-1")
	applyScreen(screen, screen.handleWatchTick())
	if *reads != before+1 {
		t.Errorf("%d reads after a change, want %d", *reads-before, 1)
	}
}

// News that arrives while a read is in flight describes a state that read
// cannot have seen, so another one follows it. Without this, the last event of
// a restart — the one that says it came back up — would be the one lost.
func TestNewsDuringAReadIsNotLost(t *testing.T) {
	stream := newWatchStream()
	screen, reads := watched(t, stream)

	// A read is in flight: the sampler refuses to start another.
	screen.sampler.read(sourceServices)
	before := *reads
	stream.change("app-web-1")
	screen.handleWatchTick()
	if *reads != before {
		t.Fatalf("a second read started while one was in flight")
	}
	if !screen.servicesStale {
		t.Fatal("the change was forgotten rather than remembered")
	}

	// The in-flight read lands: the follow-up goes out.
	applyScreen(screen, screen.Update(servicesSampleMsg{}))
	if *reads != before+1 {
		t.Errorf("%d follow-up reads after the sample landed, want 1", *reads-before)
	}
	if screen.servicesStale {
		t.Error("the screen still thinks it owes a read")
	}
}

// Watching is an optimisation. A host whose daemon refuses the stream keeps
// the timer and says nothing about it: there is nothing an operator could do.
func TestAFailedStreamIsSilentAndLeavesTheTimer(t *testing.T) {
	screen := New(Config{
		Services: func() ([]compose.Service, error) {
			return []compose.Service{{Service: "web", Project: "app"}}, nil
		},
		Watch: func(string) (operations.Feed, error) {
			return operations.Feed{}, errors.New("permission denied")
		},
	}, status.New(status.Config{}))

	applyScreen(screen, screen.sampler.due())
	if screen.watch != nil || screen.watchStarting {
		t.Error("a failed stream was kept")
	}
	if got := screen.sampler.sources[sourceServices].every; got != servicesRefresh {
		t.Errorf("interval after a failed stream = %s, want the timer's %s",
			got, servicesRefresh)
	}
	if status := screen.focused().Status(); strings.Contains(status, "permission denied") {
		t.Errorf("the failure was put in front of the operator: %q", status)
	}
}

// The stream keeps draining while a log view is on screen: that is exactly
// when a container is most likely to change, and its channel must not fill.
func TestTheStreamDrainsWhileAnotherViewIsOnScreen(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)

	cmd, handled := screen.UpdateBackground(watchTickMsg{})
	if !handled || cmd == nil {
		t.Fatal("the stream stopped draining behind another view")
	}
}

// The stream already runs for the refresh; the feed panel is what the same
// events are worth on their own, which is the question a table cannot
// answer — not what is running now, but what happened a minute ago.
func TestTheFeedRecordsWhatTheStreamReported(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)

	if !strings.Contains(screen.View(), "events") {
		t.Errorf("no feed panel on a session that is watching:\n%s", screen.View())
	}

	stream.change("app-web-1")
	applyScreen(screen, screen.handleWatchTick())

	view := screen.View()
	// Named the way the table names it, and read for what the exit code
	// means rather than printed as a number nobody has to decode.
	for _, want := range []string{"web", "killed (137)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the feed does not show %q:\n%s", want, view)
		}
	}
}

// `enter` descends one level on whatever has focus. On an event that is the
// logs of the container it happened to.
func TestEnterOnAnEventOpensThatContainersLogs(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)
	stream.change("app-web-1")
	applyScreen(screen, screen.handleWatchTick())

	// The ring is band, table, feed, and focus starts on the table.
	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	if screen.focus != screen.eventsIndex {
		t.Fatalf("tab did not reach the feed: focus is %d", screen.focus)
	}

	cmd := screen.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on an event opened nothing")
	}
	msg, ok := cmd().(OpenLogsMsg)
	if !ok {
		t.Fatalf("enter produced %T, want a request to open logs", cmd())
	}
	// The daemon reports the container; the logs are asked for by service.
	if msg.Service != "web" {
		t.Errorf("opened %q, want the service behind app-web-1", msg.Service)
	}
}

// A terminal too short for both loses the satellite, and focus must not be
// left pointing at a panel nobody can see.
func TestFocusLeavesTheFeedWhenTheTerminalLosesIt(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)

	screen.Update(tea.KeyMsg{Type: tea.KeyTab})
	if screen.focus != screen.eventsIndex {
		t.Fatalf("tab did not reach the feed: focus is %d", screen.focus)
	}

	screen.SetSize(120, 12)
	if screen.focus == screen.eventsIndex {
		t.Error("focus stayed on a panel the terminal no longer draws")
	}
	if strings.Contains(screen.View(), "─ events") {
		t.Errorf("the feed was drawn on a terminal with no room for it:\n%s", screen.View())
	}
	// And tab must not walk back onto it.
	for range 4 {
		screen.Update(tea.KeyMsg{Type: tea.KeyTab})
		if screen.focus == screen.eventsIndex {
			t.Fatal("tab landed on the feed while it is off the screen")
		}
	}
}

// A session with no stream has no feed panel at all: the rows belong to the
// table rather than to an empty box that will never fill.
func TestNoFeedPanelWithoutAStream(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 120, height: 30,
		services: serviceList("web")})
	sampleAll(screen)
	if screen.eventsIndex != -1 {
		t.Error("a feed panel joined the ring with nothing to feed it")
	}
	if strings.Contains(screen.View(), "─ events") {
		t.Errorf("a feed panel was drawn:\n%s", screen.View())
	}
}

package home

// Watching the daemon instead of asking it.
//
// A five-second `ps` on an idle deployment is twelve round-trips a minute to
// rediscover that nothing changed. The daemon already knows, and will say so:
// one filtered stream costs nothing while nothing happens, and puts a
// container that died on screen as soon as it dies rather than within the
// next interval.
//
// The timer does not go away, it steps back. While the stream is up, the
// service list is re-read on a slow safety net — for the changes no event
// describes (a health check that flips a column without an action, a daemon
// that dropped an event) and so that a stream which quietly stopped
// delivering cannot freeze the table indefinitely.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/operations"
)

const (
	// eventsDrainInterval is how often pending events are applied. Events
	// are rare and small; this only has to be fast enough that a restart
	// feels immediate.
	eventsDrainInterval = 250 * time.Millisecond
	// maxEventsPerTick bounds one drain, like the log view's, so a burst —
	// `compose up` on a large project — cannot starve input handling.
	maxEventsPerTick = 1_000
	// watchRetryFirst is how long a stream that ended as soon as it opened, or
	// was refused, waits before it is opened again; each further one doubles
	// it, up to watchRetryMax. Without the wait a daemon that drops the stream
	// at once was asked for it on every read, and the read is asked for every
	// five seconds once the stream is gone.
	watchRetryFirst = servicesRefresh
	// watchRetryMax is the safety net's own interval: past it, the stream
	// being down costs the table nothing it was not already paying.
	watchRetryMax = servicesWatched
	// watchSettled is how long a stream has to have stayed up for its end to
	// be news rather than a pattern: a daemon restarted, a link that dropped
	// once. Its replacement is opened on the next good read.
	watchSettled = servicesWatched
)

type (
	// watchFeedMsg is the outcome of starting the stream.
	watchFeedMsg struct {
		feed operations.Feed
		err  error
	}
	// watchTickMsg drains what the stream has delivered.
	watchTickMsg struct{}
)

func watchTick() tea.Cmd {
	return tea.Tick(eventsDrainInterval, func(time.Time) tea.Msg { return watchTickMsg{} })
}

// startWatching opens the stream once there is a project to scope it to. It
// is called after every good service sample and does nothing while a stream
// is up or being opened: the project name is not known until `ps` has
// answered once, and a stream that ended is reopened by the next read that
// succeeds — not before the wait its predecessors earned is over.
func (m *Model) startWatching() tea.Cmd {
	if m.info.Watch == nil || m.watch != nil || m.watchStarting || m.project == "" {
		return nil
	}
	if m.sampler.now().Before(m.watchRetryAt) {
		return nil
	}
	m.watchStarting = true
	watch, project := m.info.Watch, m.project
	return func() tea.Msg {
		feed, err := watch(project)
		return watchFeedMsg{feed: feed, err: err}
	}
}

func (m *Model) applyWatchFeed(msg watchFeedMsg) tea.Cmd {
	m.watchStarting = false
	if msg.err != nil {
		// Watching is an optimisation, not a capability the screen needs:
		// the timer is still there, and saying so in the footer would put an
		// error where the operator can do nothing about it. Asking again on
		// every read would be paying for a refusal every five seconds.
		m.backOffWatching()
		return nil
	}
	feed := msg.feed
	m.watch = &feed
	m.watchOpened = m.sampler.now()
	m.events.SetWatching(true)
	m.sampler.setInterval(sourceServices, servicesWatched)
	return watchTick()
}

// drainWatch applies what the stream delivered, reporting whether it ended.
func (m *Model) drainWatch() (changed bool, ended bool) {
	for range maxEventsPerTick {
		select {
		case event, ok := <-m.watch.Events:
			if !ok {
				return changed, true
			}
			switch event.Kind {
			case operations.EventChange:
				changed = true
				// The same event does two things: it says the table is out
				// of date, and it is itself worth reading. The second is
				// what the feed panel is for, and it costs nothing extra —
				// the stream is already running for the first.
				m.events.Add(event.Change, time.Now())
			case operations.EventExit:
				return changed, true
			}
		default:
			return changed, false
		}
	}
	return changed, false
}

func (m *Model) handleWatchTick() tea.Cmd {
	if m.watch == nil {
		return nil
	}
	changed, ended := m.drainWatch()
	if ended {
		// The stream stopped on its own — the daemon went away, the link
		// dropped. Go back to asking, at the cadence that assumes nobody is
		// telling us anything, and let that cadence say when: a read forced
		// here, with the daemon gone, failed at once and was followed by a
		// fresh stream that ended at once, four times a second.
		lived := m.sampler.now().Sub(m.watchOpened)
		m.stopWatching()
		if lived < watchSettled {
			m.backOffWatching()
		} else {
			m.watchRetry, m.watchRetryAt = 0, time.Time{}
		}
		return nil
	}
	if !changed {
		return watchTick()
	}
	// Something happened. If a read is already in flight it is older than
	// the news, so remember to read again when it lands.
	cmd := m.sampler.read(sourceServices)
	if cmd == nil {
		m.servicesStale = true
	}
	return tea.Batch(cmd, watchTick())
}

// backOffWatching puts off the next attempt at the stream, twice as long as
// the last time it was put off, up to the safety net's interval.
func (m *Model) backOffWatching() {
	m.watchRetry = min(max(m.watchRetry*2, watchRetryFirst), watchRetryMax)
	m.watchRetryAt = m.sampler.now().Add(m.watchRetry)
}

// stopWatching tears the stream down and gives the timer its short interval
// back. Safe to call twice.
func (m *Model) stopWatching() {
	if m.watch != nil {
		m.watch.Stop()
		m.watch = nil
	}
	m.watchStarting = false
	if m.events != nil {
		// The feed keeps what it caught: those events still happened, and
		// an empty panel would say the opposite.
		m.events.SetWatching(false)
	}
	m.sampler.setInterval(sourceServices, servicesRefresh)
}

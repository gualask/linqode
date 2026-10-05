package tui

// What is still running on the host when the application stops.
//
// Every remote stream the screen starts — the log view's feed, the daemon's
// event stream, the live stats panel — is stopped by whatever owns it when it
// is closed, and quitting closes nothing: Bubble Tea returns from Run with the
// models as they were, and a stream that was open is still open. So is one
// whose start was still in flight, and that one has no owner at all, because
// the message that would have handed it over can no longer be delivered.
//
// The owners cannot fix that between them, and nothing here asks them to.
// Every stream is registered as the backend hands it out, and whatever is
// registered when the program returns is stopped; a start that lands after
// that is stopped as it lands.

import (
	"sync"

	"github.com/gualask/linqode/internal/operations"
)

type streams struct {
	mu     sync.Mutex
	open   map[uint64]func()
	next   uint64
	closed bool
}

func newStreams() *streams {
	return &streams{open: map[uint64]func(){}}
}

// add registers a feed and returns it with a Stop that unregisters it. The
// Stop is safe to call more than once, since a stream can now be stopped both
// by its owner and by the teardown, and a feed added after the teardown is
// stopped at once.
func (s *streams) add(feed operations.Feed) operations.Feed {
	if feed.Stop == nil {
		return feed
	}
	stop := feed.Stop
	var once sync.Once
	s.mu.Lock()
	id := s.next
	s.next++
	closed := s.closed
	feed.Stop = func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.open, id)
			s.mu.Unlock()
			stop()
		})
	}
	if !closed {
		s.open[id] = feed.Stop
	}
	s.mu.Unlock()
	if closed {
		feed.Stop()
	}
	return feed
}

// stopAll stops every stream still registered, and every one registered from
// now on.
func (s *streams) stopAll() {
	s.mu.Lock()
	s.closed = true
	stops := make([]func(), 0, len(s.open))
	for _, stop := range s.open {
		stops = append(stops, stop)
	}
	s.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
}

// track wraps one feed-returning call so that what it returns is registered.
func track[A any](s *streams, start func(A) (operations.Feed, error)) func(A) (operations.Feed, error) {
	if start == nil {
		return nil
	}
	return func(arg A) (operations.Feed, error) {
		feed, err := start(arg)
		return s.add(feed), err
	}
}

// wrap registers every stream the backend starts.
func (s *streams) wrap(backend Backend) Backend {
	if logs := backend.Logs; logs != nil {
		backend.Logs = func(service string, tail int) (operations.Feed, error) {
			feed, err := logs(service, tail)
			return s.add(feed), err
		}
	}
	if action := backend.Action; action != nil {
		backend.Action = func(verb operations.ServiceAction, service string) (operations.Feed, error) {
			feed, err := action(verb, service)
			return s.add(feed), err
		}
	}
	if live := backend.LiveStats; live != nil {
		backend.LiveStats = func() (operations.Feed, error) {
			feed, err := live()
			return s.add(feed), err
		}
	}
	backend.Script = track(s, backend.Script)
	backend.AdHoc = track(s, backend.AdHoc)
	backend.Watch = track(s, backend.Watch)
	return backend
}

package status

import "github.com/gualask/linqode/internal/operations"

// Config says which readings exist for this host. The panel does not fetch
// any of them: the screen owns the cadence and hands samples over, so that
// what runs on the server is decided in one place (see internal/tui/home).
type Config struct {
	// Stats reports whether container readings are available at all. The
	// columns exist whenever something can fill them, and show "-" until
	// the first sample lands.
	Stats bool
	// LiveStats reports whether the on-demand stream is available.
	LiveStats bool
	// Unavailable is why this host cannot run compose commands at all, empty
	// when it can. It is not an error: an error is a refresh that failed and
	// may succeed next time, and this will not change while the session is
	// open. The panel says it in place of the table rather than sitting empty
	// or claiming to be loading something that is never coming.
	Unavailable string
}

// OpenStatsMsg asks the application for the live stats stream, which only it
// can start.
type OpenStatsMsg struct{}

type StatsFeedMsg struct {
	Feed operations.Feed
	Err  error
}

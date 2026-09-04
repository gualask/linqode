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
}

// OpenStatsMsg asks the application for the live stats stream, which only it
// can start.
type OpenStatsMsg struct{}

type StatsFeedMsg struct {
	Feed operations.Feed
	Err  error
}

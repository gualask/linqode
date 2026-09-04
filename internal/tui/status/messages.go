package status

import (
	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

// Config supplies the panel's background operations. What the session is —
// target, project directory, configured scripts — belongs to the screen that
// composes this panel, not to the table itself.
type Config struct {
	Services  func() ([]compose.Service, error)
	Stats     func() ([]compose.ContainerStats, error)
	LiveStats bool
}

// OpenStatsMsg asks the application for the live stats stream, which only it
// can start.
type OpenStatsMsg struct{}

type StatsFeedMsg struct {
	Feed operations.Feed
	Err  error
}

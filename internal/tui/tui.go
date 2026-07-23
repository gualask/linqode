// Package tui is the Bubble Tea application: views, keymaps, state.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
)

// Info is the static context for the session, shown in the header.
type Info struct {
	// Target is the `user@host` the session is connected to.
	Target string
	// ComposeDir is the remote directory of the compose project, if
	// configured.
	ComposeDir string
}

// Fetch loads the current service list. It blocks on the SSH round-trip,
// so it is always called from a background command, never from the UI
// loop.
type Fetch func() ([]compose.Service, error)

// Run shows the compose status view until the user quits.
func Run(info Info, fetch Fetch) error {
	_, err := tea.NewProgram(newStatusModel(info, fetch), tea.WithAltScreen()).Run()
	return err
}

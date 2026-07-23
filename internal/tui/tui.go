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
	// Scripts are the predefined commands from the config, sorted by name.
	Scripts []Script
}

// Script is a predefined command runnable from the status view.
type Script struct {
	Name    string
	Command string
}

// Fetch loads the current service list. It blocks on the SSH round-trip,
// so it is always called from a background command, never from the UI
// loop.
type Fetch func() ([]compose.Service, error)

// Exec starts a remote command (log follow, later actions and scripts)
// streaming into a LogFeed. Like Fetch, it blocks briefly and runs in a
// background command.
type Exec func(command string) (LogFeed, error)

// LogEventKind discriminates followed-stream events.
type LogEventKind int

const (
	// LogLine is a complete log line (the followed command's stdout).
	LogLine LogEventKind = iota
	// LogStderrLine is a diagnostic line the remote command wrote to
	// stderr.
	LogStderrLine
	// LogEnded reports that the stream terminated remotely; the events
	// channel closes after it.
	LogEnded
)

// LogEvent is one event of a followed log stream, ready for display.
type LogEvent struct {
	Kind LogEventKind
	Text string
	// ExitCode accompanies LogEnded; -1 when the remote reported none.
	ExitCode int
}

// LogFeed is the consumer end of a followed log stream. Stop cancels the
// remote command; it must be idempotent.
type LogFeed struct {
	Events <-chan LogEvent
	Stop   func()
}

// openFollowMsg asks the app to start command remotely and follow its
// output.
type openFollowMsg struct {
	title   string
	command string
}

// feedMsg is the outcome of starting a follow.
type feedMsg struct {
	title string
	feed  LogFeed
	err   error
}

// closeFollowMsg asks the app to close the log view and return to status.
type closeFollowMsg struct{}

type appModel struct {
	info    Info
	exec    Exec
	status  statusModel
	logView *logsModel

	width, height int
}

// Run shows the application until the user quits.
func Run(info Info, fetch Fetch, exec Exec) error {
	app := appModel{info: info, exec: exec, status: newStatusModel(info, fetch)}
	_, err := tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

func (m appModel) Init() tea.Cmd {
	return m.status.init()
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.status.setSize(msg.Width, msg.Height)
		if m.logView != nil {
			m.logView.setSize(msg.Width, msg.Height)
		}
		return m, nil

	case openFollowMsg:
		exec := m.exec
		return m, func() tea.Msg {
			feed, err := exec(msg.command)
			return feedMsg{title: msg.title, feed: feed, err: err}
		}

	case feedMsg:
		if msg.err != nil {
			m.status.setError(msg.err.Error())
			return m, nil
		}
		view := newLogsModel(m.info.Target, msg.title, msg.feed)
		view.setSize(m.width, m.height)
		m.logView = &view
		return m, view.init()

	case closeFollowMsg:
		if m.logView != nil {
			m.logView.feed.Stop()
			m.logView = nil
		}
		// Refresh on return so an action's effect is visible immediately.
		return m, m.status.refresh()

	case autoTickMsg:
		if m.logView != nil {
			return m, autoTick() // keep the timer alive, skip the fetch
		}

	case servicesMsg:
		return m, m.status.update(msg)
	}

	if m.logView != nil {
		return m, m.logView.update(msg)
	}
	return m, m.status.update(msg)
}

func (m appModel) View() string {
	if m.logView != nil {
		return m.logView.view()
	}
	return m.status.view()
}

// Package tui is the Bubble Tea application: views, keymaps, state.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
)

// Info is the static context for the session, shown in the header.
type Info struct {
	// Target is the `user@host` the session is connected to.
	Target string
	// ComposeDir is the remote project directory shown in the header.
	ComposeDir string
	// Scripts are the predefined commands from the config, sorted by name.
	Scripts []operations.Script
}

// Fetch loads the current service list. It blocks on the SSH round-trip,
// so it is always called from a background command, never from the UI
// loop.
type Fetch func() ([]compose.Service, error)

// FetchHost samples the remote machine's resource usage for the header. Nil
// disables the resource line entirely — the escape hatch for hosts where
// even a cheap extra command is unwelcome. Like Fetch, it runs in a
// background command.
type FetchHost func() (host.Metrics, error)

// FetchStats takes one sample of the containers' resource usage, for the
// status table's CPU and MEM columns. Nil leaves those columns out, the
// same escape hatch FetchHost is for the resource panel. Like Fetch, it
// runs in a background command — it is the slowest of the three, since
// docker needs a second of sampling to derive a CPU percentage.
type FetchStats func() ([]compose.ContainerStats, error)

// Backend adapts shared operations to the TUI's background-command model.
// Action and Script are constrained operations; AdHoc is the explicitly
// human-only `!` path.
type Backend struct {
	Services      Fetch
	Host          FetchHost
	Stats         FetchStats
	Logs          func(service string, tail int) (operations.Feed, error)
	LiveStats     func() (operations.Feed, error)
	ActionPreview func(action operations.ServiceAction, service string) string
	Action        func(action operations.ServiceAction, service string) (operations.Feed, error)
	Script        func(name string) (operations.Feed, error)
	AdHoc         func(command string) (operations.Feed, error)
}

type openLogsMsg struct{ title, service string }
type openActionMsg struct {
	title, service string
	action         operations.ServiceAction
}
type openScriptMsg struct{ title, name string }
type openAdHocMsg struct{ title, command string }

// feedMsg is the outcome of starting a follow.
type feedMsg struct {
	title string
	feed  operations.Feed
	err   error
}

// closeFollowMsg asks the app to close the log view and return to status.
type closeFollowMsg struct{}

// openStatsMsg asks the app to start the status view's live resource stream.
type openStatsMsg struct{}

// statsFeedMsg is the outcome of starting that stream.
type statsFeedMsg struct {
	feed operations.Feed
	err  error
}

type appModel struct {
	info    Info
	backend Backend
	status  statusModel
	logView *logsModel

	width, height int
}

// Run shows the application until the user quits. A nil Backend.Host leaves
// the system panel out, and nil Backend.Stats leaves the soft resource columns
// out.
func Run(info Info, backend Backend) error {
	status := newStatusModel(info, backend.Services)
	status.hostFetch = backend.Host
	status.statsFetch = backend.Stats
	status.liveStats = backend.LiveStats != nil
	status.actionPreview = backend.ActionPreview
	app := appModel{info: info, backend: backend, status: status}
	_, err := tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

func (m appModel) Init() tea.Cmd {
	return m.status.init()
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg)
	case openLogsMsg:
		return m.openLogs(msg)
	case openActionMsg:
		return m.openAction(msg)
	case openScriptMsg:
		return m.openScript(msg)
	case openAdHocMsg:
		return m.openAdHoc(msg)
	case feedMsg:
		return m.applyFeed(msg)
	case closeFollowMsg:
		return m.closeFeed()
	case autoTickMsg:
		return m.handleAutoTick(msg)
	case statsPollMsg:
		return m.handleStatsPoll(msg)
	case openStatsMsg:
		return m.openLiveStats()
	// These always belong to the status view, even while the log view is on
	// screen: the live stream keeps running so returning to the table does
	// not pay docker's sampling latency again, and a sample already in
	// flight is worth applying.
	case servicesMsg, hostMsg, statsFeedMsg, statsTickMsg, statsSampleMsg:
		return m, m.status.update(msg)
	}
	return m.routeVisibleView(msg)
}

func (m appModel) resize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	m.status.setSize(msg.Width, msg.Height)
	if m.logView != nil {
		m.logView.setSize(msg.Width, msg.Height)
	}
	return m, nil
}

func (m appModel) openLogs(msg openLogsMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.title, func() (operations.Feed, error) {
		return m.backend.Logs(msg.service, logTail)
	})
}

func (m appModel) openAction(msg openActionMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.title, func() (operations.Feed, error) {
		return m.backend.Action(msg.action, msg.service)
	})
}

func (m appModel) openScript(msg openScriptMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.title, func() (operations.Feed, error) {
		return m.backend.Script(msg.name)
	})
}

func (m appModel) openAdHoc(msg openAdHocMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.title, func() (operations.Feed, error) {
		return m.backend.AdHoc(msg.command)
	})
}

func (m appModel) applyFeed(msg feedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status.setError(msg.err.Error())
		return m, nil
	}
	view := newLogsModel(m.info.Target, msg.title, msg.feed)
	view.setSize(m.width, m.height)
	m.logView = &view
	return m, view.init()
}

func (m appModel) closeFeed() (tea.Model, tea.Cmd) {
	if m.logView != nil {
		m.logView.feed.Stop()
		m.logView = nil
	}
	// Refresh on return so an action's effect is visible immediately.
	return m, m.status.refresh()
}

func (m appModel) handleAutoTick(msg autoTickMsg) (tea.Model, tea.Cmd) {
	if m.logView != nil {
		return m, autoTick() // keep the timer alive, skip the fetch
	}
	return m, m.status.update(msg)
}

func (m appModel) handleStatsPoll(msg statsPollMsg) (tea.Model, tea.Cmd) {
	// A sample occupies the server for ~2 s, which is not worth paying for a
	// table nobody is looking at.
	if m.logView != nil {
		return m, statsPollTick()
	}
	return m, m.status.update(msg)
}

func (m appModel) openLiveStats() (tea.Model, tea.Cmd) {
	follow := m.backend.LiveStats
	return m, func() tea.Msg {
		feed, err := follow()
		return statsFeedMsg{feed: feed, err: err}
	}
}

func (m appModel) routeVisibleView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.logView != nil {
		return m, m.logView.update(msg)
	}
	return m, m.status.update(msg)
}

func startFeed(title string, start func() (operations.Feed, error)) tea.Cmd {
	return func() tea.Msg {
		feed, err := start()
		return feedMsg{title: title, feed: feed, err: err}
	}
}

func (m appModel) View() string {
	if m.logView != nil {
		return m.logView.view()
	}
	return m.status.view()
}

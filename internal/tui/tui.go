// Package tui is the Bubble Tea application: views, keymaps, state.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/follow"
	"github.com/gualask/linqode/internal/tui/home"
	"github.com/gualask/linqode/internal/tui/status"
)

// Info is the static context for the session, shown in the header.
type Info struct {
	// Target is the `user@host` the session is connected to.
	Target string
	// ComposeDir is the remote project directory shown in the header.
	ComposeDir string
	// Scripts are the predefined commands from the config, sorted by name.
	Scripts []operations.Script
	// OS is what the host calls itself, from /etc/os-release. Empty on a host
	// that did not say.
	OS string
	// ComposeUnavailable is why this host cannot run compose commands, empty
	// when it can. The connect-time probe establishes it once; it names a
	// permanent condition, not a refresh that failed, and the panel it stands
	// in for says so instead of sitting empty.
	ComposeUnavailable string
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

// FetchCgroups reads the containers' resource counters off the kernel, for
// the status table's CPU, MEM, NET and IO columns. Nil leaves those columns
// out, the same escape hatch FetchHost is for the meters. It takes the
// current services because the counters are addressed by container id and by
// process, both of which `ps` and its inspect already reported.
//
// The readings are cumulative: it takes two samples to say what a percentage
// is, which is exactly the work `docker stats` spends two seconds doing on
// the server.
type FetchCgroups func(services []compose.Service) (compose.CgroupSample, error)

const logTail = 200

// Backend adapts shared operations to the TUI's background-command model.
// Action and Script are constrained operations; AdHoc is the explicitly
// human-only `!` path.
type Backend struct {
	Services Fetch
	Host     FetchHost
	Stats    FetchCgroups
	// Processes reads the machine's process table. It is the on-demand tier:
	// nothing reads it until the system view is opened, because it costs
	// about 220 bytes per process and answers a question nobody has asked
	// until they are looking at it.
	Processes func() (host.ProcessSample, error)
	// DiskUsage asks the daemon what it is holding. Same tier as Processes,
	// and the one reading here that is genuinely slow on a real host.
	DiskUsage func() ([]compose.DiskUsage, error)
	// GPUs reads the graphics cards, on the same tier for the same reason.
	GPUs      func() ([]host.GPU, error)
	Logs      func(service string, tail int) (operations.Feed, error)
	LiveStats func() (operations.Feed, error)
	// Watch streams the daemon's changes to the project's containers, which
	// is what makes the table react rather than poll. Nil leaves the screen
	// on its timer.
	Watch         func(project string) (operations.Feed, error)
	ActionPreview func(action operations.ServiceAction, service string) string
	Action        func(action operations.ServiceAction, service string) (operations.Feed, error)
	Script        func(name string) (operations.Feed, error)
	AdHoc         func(command string) (operations.Feed, error)
}

// feedMsg is the outcome of starting a follow.
type feedMsg struct {
	title string
	feed  operations.Feed
	err   error
}

type appModel struct {
	info       Info
	backend    Backend
	home       *home.Model
	followView *follow.Model

	width, height int
}

// Run shows the application until the user quits.
//
// A nil field is a capability this session does not have, and the screen is
// built around what is left rather than around what is missing: nil
// Backend.Host leaves the meters out, nil Backend.Stats the soft resource
// columns, nil Backend.Services the compose table. Two things decide which
// are nil — the `host_metrics` opt-out in the config, and what the
// connect-time probe found on the host — and the screen is told the reason
// for the second so it can say it where the panel would have been.
func Run(info Info, backend Backend) error {
	services := status.New(status.Config{
		Stats:       backend.Stats != nil,
		LiveStats:   backend.LiveStats != nil,
		Unavailable: info.ComposeUnavailable,
	})
	screen := home.New(home.Config{
		Target:             info.Target,
		ComposeDir:         info.ComposeDir,
		OS:                 info.OS,
		Scripts:            info.Scripts,
		ComposeUnavailable: info.ComposeUnavailable,
		ActionPreview:      backend.ActionPreview,
		Services:           backend.Services,
		Host:               backend.Host,
		Stats:              backend.Stats,
		Processes:          backend.Processes,
		DiskUsage:          backend.DiskUsage,
		GPUs:               backend.GPUs,
		Watch:              backend.Watch,
	}, services)
	app := appModel{info: info, backend: backend, home: screen}
	_, err := tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

func (m appModel) Init() tea.Cmd {
	return m.home.Init()
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg)
	case home.OpenLogsMsg:
		return m.openLogs(msg)
	case home.OpenActionMsg:
		return m.openAction(msg)
	case home.OpenScriptMsg:
		return m.openScript(msg)
	case home.OpenAdHocMsg:
		return m.openAdHoc(msg)
	case feedMsg:
		return m.applyFeed(msg)
	case follow.CloseMsg:
		return m.closeFeed()
	case status.OpenStatsMsg:
		return m.openLiveStats()
	}
	return m.routeVisibleView(msg)
}

func (m appModel) resize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	m.home.SetSize(msg.Width, msg.Height)
	if m.followView != nil {
		m.followView.SetSize(msg.Width, msg.Height)
	}
	return m, nil
}

func (m appModel) openLogs(msg home.OpenLogsMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.Title, func() (operations.Feed, error) {
		return m.backend.Logs(msg.Service, logTail)
	})
}

func (m appModel) openAction(msg home.OpenActionMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.Title, func() (operations.Feed, error) {
		return m.backend.Action(msg.Action, msg.Service)
	})
}

func (m appModel) openScript(msg home.OpenScriptMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.Title, func() (operations.Feed, error) {
		return m.backend.Script(msg.Name)
	})
}

func (m appModel) openAdHoc(msg home.OpenAdHocMsg) (tea.Model, tea.Cmd) {
	return m, startFeed(msg.Title, func() (operations.Feed, error) {
		return m.backend.AdHoc(msg.Command)
	})
}

func (m appModel) applyFeed(msg feedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.home.SetError(msg.err.Error())
		return m, nil
	}
	view := follow.New(m.info.Target, msg.title, msg.feed)
	view.SetSize(m.width, m.height)
	m.followView = view
	return m, view.Init()
}

func (m appModel) closeFeed() (tea.Model, tea.Cmd) {
	if m.followView != nil {
		m.followView.Stop()
		m.followView = nil
	}
	// Refresh on return so an action's effect is visible immediately.
	return m, m.home.Refresh()
}

func (m appModel) openLiveStats() (tea.Model, tea.Cmd) {
	follow := m.backend.LiveStats
	return m, func() tea.Msg {
		feed, err := follow()
		return status.StatsFeedMsg{Feed: feed, Err: err}
	}
}

func (m appModel) routeVisibleView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.followView != nil {
		if cmd, handled := m.home.UpdateBackground(msg); handled {
			return m, cmd
		}
		return m, m.followView.Update(msg)
	}
	return m, m.home.Update(msg)
}

func startFeed(title string, start func() (operations.Feed, error)) tea.Cmd {
	return func() tea.Msg {
		feed, err := start()
		return feedMsg{title: title, feed: feed, err: err}
	}
}

func (m appModel) View() string {
	if m.followView != nil {
		return m.followView.View()
	}
	return m.home.View()
}

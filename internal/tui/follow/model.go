// Package follow implements the TUI view for followed remote command feeds.
package follow

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/operations"
)

const (
	// logCapacity is the scrollback kept in memory per followed command.
	logCapacity = 10_000
	// maxEventsPerTick bounds how many feed events one drain applies.
	maxEventsPerTick = 5_000
	drainInterval    = 100 * time.Millisecond
	// statsWidth is the width of the stats side panel; topValues is how
	// many top values it lists.
	statsWidth = 28
	topValues  = 8
	// statsBar is how wide a count's bar is drawn: eight cells hold sixty-four
	// widths, which is finer than anybody reads a share at, and leaves the
	// names beside them most of the panel.
	statsBar = 8
)

type drainTickMsg struct{}

// CloseMsg asks the application shell to close the followed feed.
type CloseMsg struct{}

func drainTick() tea.Cmd {
	return tea.Tick(drainInterval, func(time.Time) tea.Msg { return drainTickMsg{} })
}

// Model owns one followed remote command feed and its presentation state.
type Model struct {
	target string
	// title labels the header: `logs: web`, `restart: web`, …
	title string
	feed  operations.Feed
	store *logs.Store

	// scroll is the index of the top visible line; recomputed on view when
	// following.
	follow   bool
	scroll   int
	viewport int
	width    int
	height   int

	feedDone bool // events channel closed
	ended    bool
	exitCode int // -1 = none reported
	// stderrNotice is the last stderr line from the remote command
	// (compose diagnostics).
	stderrNotice string

	input     inputMode
	inputText string
	query     string
	matchLine int // current match, anchor for n/N; -1 = none
	notice    string

	// structured: nil follows JSONL auto-detection, otherwise a manual
	// override (`s`).
	structured *bool
	showStats  bool
	topField   string

	// now is the clock lines are stamped with as they arrive and the
	// timeline ends at. A field rather than a call so a test can say when
	// now is.
	now func() time.Time
}

// New creates a followed-feed model.
func New(target, title string, feed operations.Feed) *Model {
	return &Model{
		target:    target,
		title:     title,
		feed:      feed,
		store:     logs.NewStore(logCapacity),
		follow:    true,
		exitCode:  -1,
		matchLine: -1,
		now:       time.Now,
	}
}

// Init starts the bounded feed-drain timer.
func (m *Model) Init() tea.Cmd {
	return drainTick()
}

// SetSize updates the terminal dimensions used for navigation and rendering.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.viewport = max(height-2, 1) // header (1) + footer (1)
}

// Stop cancels the underlying remote feed.
func (m *Model) Stop() {
	m.feed.Stop()
}

// Update applies a feed tick or user input.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case drainTickMsg:
		m.drain()
		if m.feedDone {
			return nil
		}
		return drainTick()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return nil
}

// drain applies pending feed events to the store, at most maxEventsPerTick.
func (m *Model) drain() {
	for range maxEventsPerTick {
		if !m.drainNextEvent() {
			return
		}
	}
}

func (m *Model) drainNextEvent() bool {
	select {
	case event, open := <-m.feed.Events:
		if !open {
			m.feedDone = true
			m.ended = true
			return false
		}
		m.applyFeedEvent(event)
		return true
	default:
		return false
	}
}

func (m *Model) applyFeedEvent(event operations.Event) {
	switch event.Kind {
	case operations.EventLog, operations.EventStdout:
		m.adjustForDroppedLines(m.store.PushAt(event.Text, m.now()))
	case operations.EventStderr:
		m.stderrNotice = event.Text
		m.adjustForDroppedLines(m.store.PushAt(event.Text, m.now()))
	case operations.EventExit:
		m.ended = true
		m.exitCode = event.ExitCode
	}
}

func (m *Model) adjustForDroppedLines(dropped int) {
	if dropped <= 0 {
		return
	}
	m.scroll = max(m.scroll-dropped, 0)
	if m.matchLine >= 0 {
		m.matchLine = max(m.matchLine-dropped, -1)
	}
}

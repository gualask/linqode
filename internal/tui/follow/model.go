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
	// backlogMax is the longest the opening burst of a log follow is taken
	// to last. The burst ends at the first drain that finds nothing new; a
	// service logging faster than a line per drain never leaves one, and
	// past this its lines are live whatever the drains say.
	backlogMax = 2 * time.Second
	// statsWidth is the width of the stats side panel; topValues is how
	// many top values it lists.
	statsWidth = 28
	topValues  = 8
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
	// statsFocus is whether the keys go to the stats panel — its cursor, the
	// filter it picks — rather than to the log.
	statsFocus bool
	// topField is the field the panel counts values of. Empty until the
	// panel first finds one worth counting, which it picks itself; `t` steps
	// to the next.
	topField string
	// picked is the row the panel's cursor is on, by what it names rather
	// than by where it is: the counts reorder as lines arrive, and a cursor
	// kept by position would slide onto another row under the operator's
	// finger. cursor is where it was, for when the row it named is gone.
	picked pick
	cursor int

	// backlogFrom is when the first log line arrived, which opened the
	// backlog; zero until one has. backlogOpen is whether it still is.
	backlogFrom time.Time
	backlogOpen bool

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
	applied := 0
	for range maxEventsPerTick {
		if !m.drainNextEvent() {
			break
		}
		applied++
	}
	m.closeBacklog(applied)
}

// closeBacklog ends the opening burst of a log follow at the first drain
// that finds nothing new, or when the feed ends, or backlogMax after it
// began — the lines docker replays for `--tail` arrive as fast as the
// connection carries them, and a pause of a whole drain interval is the
// first sign the replay is over.
//
// A drain that merely empties the channel is not that sign: a large tail
// over a slow link arrives in pieces, and the gap between two of them is
// shorter than a drain but still inside the replay.
func (m *Model) closeBacklog(applied int) {
	if !m.backlogOpen {
		return
	}
	if applied == 0 || m.feedDone || m.now().Sub(m.backlogFrom) >= backlogMax {
		m.store.CloseBacklog()
		m.backlogOpen = false
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
	case operations.EventLog:
		// Only a log follow opens with a backlog. A script's or a command's
		// output is live from its first line, and placing it by arrival is
		// exactly right.
		if m.backlogFrom.IsZero() {
			m.backlogFrom, m.backlogOpen = m.now(), true
			m.store.OpenBacklog()
		}
		m.adjustForDroppedLines(m.store.PushAt(event.Text, m.now()))
	case operations.EventStdout:
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

// Package events is the "what just happened" feed: the daemon's own report
// of containers starting, dying, being killed and changing health, as it
// arrives.
//
// It exists because the table cannot say it. A table is a statement about
// now — `web` is running, it has restarted seven times — and the question an
// operator opens this tool with is usually about a moment that has passed:
// *when* did it restart, was it killed or did it exit, did the health check
// flip before or after the deploy. Two readings of the same table cannot
// answer that; a feed can, and the stream feeding it is already running for
// the refresh (internal/tui/home/events.go).
//
// So this panel costs nothing new on the wire. That is also why it is a
// panel and not a log view: raw container logs on a dashboard are noise that
// scrolls and continuous bandwidth, and they are one `enter` away per
// service already.
package events

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

// depth is how many events are kept. The panel shows a handful; the rest are
// there for the scroll, and for the operator who looks up a minute after the
// thing they are chasing scrolled past.
const depth = 200

// Model is the feed. It holds what arrived and which entry is selected;
// where the events come from is the screen's business, as every other
// reading on it is.
type Model struct {
	// entries are newest first. That order is not a preference: the panel is
	// a few rows tall, and it is the newest event that must be visible
	// without anyone having scrolled to it.
	entries []compose.Event
	// services maps a container back to the service it belongs to, so the
	// feed names things the way the table above it does and `enter` can open
	// the right logs.
	services map[string]string

	// watching is whether the daemon's stream is up. When it is not, the
	// table is on its timer and this panel is a record of what the stream
	// caught before it stopped — which is worth saying rather than leaving
	// an empty box to imply nothing has happened.
	watching bool

	cursor        int
	focused       bool
	width, height int
}

func New() *Model { return &Model{services: map[string]string{}} }

// Add records one event, filling in the time the line was read when the
// daemon did not say when it happened.
//
// The selection follows the newest event while it is on the newest event,
// and stays on its entry once it has been moved off — the same rule the log
// view follows, and for the same reason: an operator reading something does
// not want it to slide away because another line arrived.
func (m *Model) Add(event compose.Event, readAt time.Time) {
	if event.At.IsZero() {
		event.At = readAt
	}
	m.entries = append([]compose.Event{event}, m.entries...)
	if len(m.entries) > depth {
		m.entries = m.entries[:depth]
	}
	if m.cursor > 0 {
		m.cursor = min(m.cursor+1, len(m.entries)-1)
	}
}

// SetServices records the container-to-service mapping the table already
// has. An event about a container that has since been destroyed keeps the
// name the daemon gave it.
func (m *Model) SetServices(services []compose.Service) {
	mapping := make(map[string]string, len(services))
	for _, service := range services {
		if service.Name != "" && service.Service != "" {
			mapping[service.Name] = service.Service
		}
	}
	m.services = mapping
}

// SetWatching records whether the daemon's stream is up.
func (m *Model) SetWatching(watching bool) { m.watching = watching }

// SelectedService is the service behind the selected event, for the screen
// to open its logs. An event about a container the project no longer has is
// not something to open.
func (m *Model) SelectedService() (string, bool) {
	if m.cursor >= len(m.entries) {
		return "", false
	}
	service, ok := m.services[m.entries[m.cursor].Container]
	return service, ok
}

func (m *Model) Title() string { return "events" }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }
func (m *Model) SetFocus(focused bool)     { m.focused = focused }

// Hints are the keys this panel answers to. Moving the selection is not among
// them: it is the arrows, `PgUp`/`PgDn` and `Home`/`End`, the same set as in
// every other list on the screen, and a line advertising what every terminal
// already sends is a line spent teaching nothing. This one advertised `j/k`,
// which the panel stopped answering when the vim aliases went — two keys
// promised on the footer and answered by nothing at all.
//
// Nor is anything offered when there is nothing under the cursor to open: an
// empty feed, or an event about a container the project no longer has. Both
// are states this panel sits in for hours rather than for a moment — an empty
// feed is a deployment where nothing has happened, which is the good case —
// so a key advertised in them is a key an operator presses and watches do
// nothing.
func (m *Model) Hints() []panel.Hint {
	// One predicate governs both this panel's keys and the screen's `c`.
	if !m.OffersServiceKeys() {
		return nil
	}
	return []panel.Hint{{Text: "enter logs", Drop: 2}}
}

// The feed is a region the keys that need a service can talk to: its entries
// are containers of the same project. Declared here so that dropping either
// method breaks the build rather than quietly taking the keys off the footer.
var _ panel.ServiceRegion = (*Model)(nil)

// OffersServiceKeys is this panel's half of panel.ServiceRegion, and its
// answer is the strict one: nothing is offered unless the cursor is on an
// event whose container the project still has.
//
// Both ways of having nothing there last. A feed with no entries is a
// deployment where nothing has happened, which is the good case and the one
// an operator is in most of the time; an event about a container that was
// destroyed is a row and a cursor with nothing behind them. Neither is a
// panel waiting for data, so neither is a reason to keep a key on the footer.
func (m *Model) OffersServiceKeys() bool {
	_, ok := m.SelectedService()
	return ok
}

// Status is this panel's half of the footer, and there is nothing for it to
// say: the footer is the keymap, and what this panel is showing belongs on its
// own rule, where it is legible without focus.
func (m *Model) Status() string { return "" }

// Summary rides on the panel's rule: how much it has caught, and whether the
// daemon is being listened to at all.
//
// The watching flag is the half that matters. Without the stream the table
// falls back to its timer, which is a slower screen rather than a broken one —
// worth knowing, and nothing an operator can act on, so it is dim and it is
// here rather than anywhere louder.
func (m *Model) Summary() string {
	parts := make([]string, 0, 2)
	switch {
	case len(m.entries) == 1:
		parts = append(parts, "1 event")
	case len(m.entries) > 1:
		parts = append(parts, fmt.Sprintf("%d events", len(m.entries)))
	}
	if !m.watching {
		parts = append(parts, theme.Dim.Render("not watching"))
	}
	return strings.Join(parts, " · ")
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "down":
		m.move(1)
	case "up":
		m.move(-1)
	case "pgdown":
		m.move(m.page())
	case "pgup":
		m.move(-m.page())
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = max(len(m.entries)-1, 0)
	}
	return nil
}

// page is how far pgup and pgdown move: the rows this panel can show at once,
// less one of overlap so the line the eye was on survives the jump.
func (m *Model) page() int { return max(m.height-1, 1) }

func (m *Model) move(delta int) {
	if len(m.entries) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), len(m.entries)-1)
}

// View is the feed, newest first, one event per line.
func (m *Model) View() string {
	if len(m.entries) == 0 {
		if m.watching {
			return theme.Dim.Render("  (watching the daemon — nothing has happened yet)")
		}
		return theme.Dim.Render("  (not watching the daemon)")
	}

	rows := m.height
	if rows <= 0 {
		rows = len(m.entries)
	}
	// The window follows the cursor, which starts at the newest event and
	// only leaves the top when someone moves it.
	first := 0
	if m.cursor >= rows {
		first = m.cursor - rows + 1
	}
	last := min(first+rows, len(m.entries))

	lines := make([]string, 0, last-first)
	for index := first; index < last; index++ {
		lines = append(lines, m.line(index))
	}
	return strings.Join(lines, "\n")
}

// nameWidth is the column the container names share. Wide enough for a
// compose name of an ordinary length; a longer one is truncated rather than
// pushing the description out of line.
const nameWidth = 22

func (m *Model) line(index int) string {
	event := m.entries[index]
	name := event.Container
	if service, ok := m.services[event.Container]; ok {
		name = service
	}
	text, style := describe(event)
	stamp := event.At.Format("15:04:05")

	if index == m.cursor {
		// The selected row is drawn plain and then filled, the way the
		// table draws its own: a background laid over text that already
		// carries colours ends wherever the first of them resets, which
		// leaves a bar the width of the timestamp instead of the row.
		plain := fmt.Sprintf(" %s  %s  %s", stamp, fit(name, nameWidth), text)
		if m.width > 0 {
			plain = fit(plain, m.width)
		}
		return m.selectionStyle().Render(plain)
	}
	return fmt.Sprintf(" %s  %s  %s",
		theme.Dim.Render(stamp), fit(name, nameWidth), style.Render(text))
}

// selectionStyle marks the selected row. A panel that does not hold focus
// keeps its place with a quiet fill rather than a lit bar, so it stops
// competing with the panel the keys are talking to.
func (m *Model) selectionStyle() lipgloss.Style {
	if m.focused {
		return theme.Reverse
	}
	return theme.SelectedIdle
}

// describe turns an event into what it is worth reading and the color that
// says how much it matters. The daemon's own vocabulary is kept — `die` is
// what it says and what an operator will find in its logs — with the part
// that is not obvious spelled out beside it.
func describe(event compose.Event) (string, lipgloss.Style) {
	switch event.Kind() {
	case "die":
		// The exit code is the whole story: 0 is a container that finished,
		// 137 is one that was killed, and the difference is not visible
		// anywhere else on the screen once the row is gone.
		switch event.ExitCode {
		case "", "0":
			return "exited (0)", theme.Dim
		case "137":
			return "killed (137)", theme.Red
		default:
			return "exited (" + event.ExitCode + ")", theme.Red
		}
	case "oom":
		return "out of memory", theme.Red
	case "kill":
		return "killed", theme.Yellow
	case "start":
		return "started", theme.Green
	case "restart":
		return "restarted", theme.Yellow
	case "stop":
		return "stopped", theme.Yellow
	case "pause":
		return "paused", theme.Yellow
	case "unpause":
		return "unpaused", theme.Green
	case "create":
		return "created", theme.Dim
	case "destroy":
		return "destroyed", theme.Dim
	case "rename":
		return "renamed", theme.Dim
	case "update":
		return "updated", theme.Dim
	case "health_status":
		switch event.Detail() {
		case "healthy":
			return "healthy", theme.Green
		case "unhealthy":
			return "unhealthy", theme.Red
		default:
			return "health: " + event.Detail(), theme.Yellow
		}
	default:
		// An action this list does not know is still worth showing: the
		// daemon added it, and a feed that hides what it does not recognize
		// is worse than one that prints a word.
		return event.Action, theme.Dim
	}
}

// fit pads or truncates to exactly width cells.
func fit(text string, width int) string {
	text = lipgloss.NewStyle().MaxWidth(width).Render(text)
	if gap := width - lipgloss.Width(text); gap > 0 {
		return text + strings.Repeat(" ", gap)
	}
	return text
}

package follow

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

// maxKeyColumn caps the width of the detail's key column, so one long
// flattened path (`request.headers.x-forwarded-for`) does not push every
// value to the right edge.
const maxKeyColumn = 24

// openDetail opens the line under the cursor in full: the log view cuts
// every line to the terminal's width, and this is where the rest of it is.
//
// It stops following. esc goes back to the log where it was left, and while
// following, where it was left is the tail, which has moved on by the time
// the line has been read — the line would come back a screen or two up with
// nothing marking it. Stopped, the cursor is still on it; `l` resumes.
func (m *Model) openDetail() {
	m.selected = m.cursorLine()
	line, ok := m.store.Line(m.selected)
	if !ok {
		return
	}
	m.follow = false
	m.detail, m.detailScroll = &line, 0
}

// handleDetailKey is a key while a line is open. The arrows scroll it, esc
// goes back to the log where it was left, q quits as everywhere else.
func (m *Model) handleDetailKey(key string) tea.Cmd {
	switch key {
	case "esc":
		m.detail = nil
	case "q", "ctrl+c":
		return tea.Quit
	case "down":
		m.scrollDetail(1)
	case "up":
		m.scrollDetail(-1)
	case "pgdown":
		m.scrollDetail(m.viewport)
	case "pgup":
		m.scrollDetail(-m.viewport)
	case "home":
		m.detailScroll = 0
	case "end":
		m.detailScroll = m.maxDetailScroll()
	}
	return nil
}

func (m *Model) scrollDetail(delta int) {
	m.detailScroll = min(max(m.detailScroll+delta, 0), m.maxDetailScroll())
}

func (m *Model) maxDetailScroll() int {
	return max(len(m.detailLines())-m.viewport, 0)
}

// detailView is the whole screen while a line is open: the detail takes the
// body, stats panel included, since it changes what the screen is about.
func (m *Model) detailView() string {
	var b strings.Builder
	b.WriteString(m.header(theme.Magenta.Render("  · line")) + "\n")

	lines := m.detailLines()
	m.detailScroll = min(m.detailScroll, m.maxDetailScroll())
	visible := lines[m.detailScroll:min(len(lines), m.detailScroll+m.viewport)]
	for len(visible) < m.viewport {
		visible = append(visible, "")
	}
	b.WriteString(strings.Join(visible, "\n"))
	b.WriteString("\n")

	hints := []panel.Hint{{Text: "esc back", Drop: 1}}
	if len(lines) > m.viewport {
		hints = append(hints, panel.Hint{Text: "↑↓ scroll", Drop: 2})
	}
	b.WriteString(panel.Footer([]panel.Hint{{Text: "q quit", Drop: 0}}, "", hints, m.width))
	return b.String()
}

// detailLines lays the open line out in full, wrapped to the terminal. A
// record reads as its header — timestamp and level — then its message, then
// one field per row, key beside value; any other line is its text, wrapped.
// The search's hits are marked here as in the log.
func (m *Model) detailLines() []string {
	width := max(m.width-2, 10) // a cell of margin each side
	line := m.detail
	record := line.Record
	if record == nil {
		return indent(m.wrapped(line.Raw, width), " ")
	}

	var out []string
	timestamp, hasTime := record.Timestamp()
	level, hasLevel := record.Level()
	message, hasMessage := record.Message()
	var head []string
	if hasTime {
		head = append(head, theme.Dim.Render(panel.Plain(timestamp)))
	}
	if hasLevel {
		head = append(head, levelStyle(level).Bold(true).Render(panel.Plain(level)))
	}
	if len(head) > 0 {
		out = append(out, " "+strings.Join(head, "  "), "")
	}
	if hasMessage {
		out = append(out, indent(m.wrapped(message, width), " ")...)
		out = append(out, "")
	}

	// The fields the header and the message already show are left out; a
	// well-known key holding something else — a second message key, say — is
	// kept, since this is the one place it can be read.
	var fields []logs.Field
	keyWidth := 0
	for _, field := range record.Fields() {
		if logs.IsWellKnownKey(field.Key) &&
			(field.Value == timestamp || field.Value == level || field.Value == message) {
			continue
		}
		fields = append(fields, field)
		keyWidth = max(keyWidth, ansi.StringWidth(panel.Plain(field.Key)))
	}
	keyWidth = min(keyWidth, maxKeyColumn)
	valueWidth := max(width-keyWidth-2, 10)
	for _, field := range fields {
		key := ansi.Truncate(panel.Plain(field.Key), keyWidth, "…")
		rows := m.wrapped(field.Value, valueWidth)
		for i, row := range rows {
			label := strings.Repeat(" ", keyWidth)
			if i == 0 {
				label = key + strings.Repeat(" ", keyWidth-ansi.StringWidth(key))
			}
			out = append(out, fmt.Sprintf(" %s  %s", theme.Dim.Render(label), row))
		}
	}
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// wrapped breaks text into rows of at most width cells — at its own line
// breaks, which the log flattens and a stack trace needs, then at spaces
// where it can and mid-word where it must — and marks the search's hits on
// each row. A hit broken across two rows is not marked.
func (m *Model) wrapped(text string, width int) []string {
	var rows []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		wrapped := ansi.Wrap(panel.Plain(line), width, "")
		for _, row := range strings.Split(wrapped, "\n") {
			rows = append(rows, highlightIn([]segment{{row, lipgloss.NewStyle()}}, m.query, false))
		}
	}
	return rows
}

func indent(rows []string, prefix string) []string {
	for i, row := range rows {
		rows[i] = prefix + row
	}
	return rows
}

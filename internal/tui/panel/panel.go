// Package panel is the chrome a focusable region of the screen wears: a
// bordered box with a title in its top rule, and the footer hints that say
// what the region answers to.
//
// The border is not decoration, and it is not free: two columns and two rows
// per box is the same cost that got the right-hand sidebar removed (September
// 2026, see internal/tui/status/hostband.go). It earns them by naming which
// region the keys are talking to, so only a region that can take focus draws
// one — a purely informative reading stays a bare band, as the host meters do.
//
// Focus is carried by color rather than by reverse video, which already marks
// the selected row inside a panel; the tokens live in the theme package, where
// the reasoning is written down.
package panel

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/theme"
)

// The narrowest and shortest box that still holds anything: two border cells
// and one of content on each axis. Below that a border would be all there is,
// so Box draws none and gives the whole space to the content.
const (
	minWidth  = 3
	minHeight = 3
)

// titleOverhead is what a title costs in the top rule beyond its own text:
// the corner, one dash, the space before, the space after, at least one dash
// after it, and the closing corner.
const titleOverhead = 6

// Box renders content inside a titled border, exactly width by height cells.
// Content is given width-2 by height-2: lines longer than that are truncated
// and lines past the last row are dropped, so a panel can never push its
// neighbours out of place by rendering more than it was allotted.
//
// A non-positive size means the caller does not know the terminal's yet; the
// content is returned as it is, the way the status view leaves its lines
// unclipped until the first WindowSizeMsg.
func Box(title, content string, focused bool, width, height int) string {
	if width <= 0 || height <= 0 {
		return content
	}
	if width < minWidth || height < minHeight {
		return fitBlock(content, width, height)
	}
	border := lipgloss.NormalBorder()
	edge := theme.BorderIdle
	if focused {
		edge = theme.BorderFocus
	}

	lines := make([]string, 0, height)
	lines = append(lines, topRule(title, focused, width))
	side := edge.Render(border.Left)
	for _, line := range blockLines(content, width-2, height-2) {
		lines = append(lines, side+line+side)
	}
	lines = append(lines, edge.Render(border.BottomLeft+
		strings.Repeat(border.Bottom, width-2)+border.BottomRight))
	return strings.Join(lines, "\n")
}

// topRule is the border's top line with the title set into it. A title that
// does not fit is truncated, and one that cannot fit at all leaves a plain
// rule: a box with no name still reads as a box.
func topRule(title string, focused bool, width int) string {
	border := lipgloss.NormalBorder()
	edge, label := theme.BorderIdle, theme.TitleIdle
	if focused {
		edge, label = theme.BorderFocus, theme.TitleFocus
	}
	room := width - titleOverhead
	if title == "" || room < 1 {
		return edge.Render(border.TopLeft +
			strings.Repeat(border.Top, width-2) + border.TopRight)
	}
	title = lipgloss.NewStyle().MaxWidth(room).Render(title)
	fill := width - titleOverhead - lipgloss.Width(title) + 1
	return edge.Render(border.TopLeft+border.Top+" ") + label.Render(title) +
		edge.Render(" "+strings.Repeat(border.Top, fill)+border.TopRight)
}

// blockLines cuts content to exactly height lines of exactly width cells,
// padding both directions. A short panel is padded rather than collapsed so
// its border closes where the layout put it, not where its content ran out.
func blockLines(content string, width, height int) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index, line := range lines {
		lines[index] = fitLine(line, width)
	}
	return lines
}

// fitBlock is the degraded rendering for a space too small to hold a border.
func fitBlock(content string, width, height int) string {
	return strings.Join(blockLines(content, width, height), "\n")
}

// fitLine truncates a line to width and pads it out to exactly that, so every
// row of a box ends under the same column.
func fitLine(line string, width int) string {
	line = lipgloss.NewStyle().MaxWidth(width).Render(line)
	if gap := width - lipgloss.Width(line); gap > 0 {
		return line + strings.Repeat(" ", gap)
	}
	return line
}

// Hint is one entry of the footer: what a key does, and how readily it is
// given up when the line does not fit.
type Hint struct {
	Text string
	// Drop orders the sacrifice: the highest Drop goes first. The last hint
	// standing is kept whatever the width, since a footer of nothing tells
	// the operator less than a footer of one thing.
	Drop int
}

// JoinHints renders hints separated by a middle dot, dropping the most
// expendable ones until the line fits. A width of zero means unknown, and
// nothing is dropped.
func JoinHints(hints []Hint, width int) string {
	hints = slices.Clone(hints)
	for {
		texts := make([]string, len(hints))
		for index, hint := range hints {
			texts[index] = hint.Text
		}
		joined := strings.Join(texts, " · ")
		if width <= 0 || len(hints) <= 1 || lipgloss.Width(joined) <= width {
			return joined
		}
		worst := 0
		for index, hint := range hints {
			if hint.Drop >= hints[worst].Drop {
				worst = index
			}
		}
		hints = slices.Delete(hints, worst, worst+1)
	}
}

// A Panel is one focusable region of the screen. The model composing them
// owns where each goes and which one holds focus; the panel owns what is
// inside it and which keys it answers to.
//
// Size and focus are set rather than passed to View because a panel needs
// both before it renders anything — the selection of an unfocused panel has
// to recede — and because Bubble Tea models are already written this way.
type Panel interface {
	// Title names the panel in its top rule.
	Title() string
	// Status is the panel's half of the footer: what it is showing, or what
	// went wrong while showing it.
	Status() string
	// Hints are the keys this panel answers to, joined into the footer
	// beside the screen's own.
	Hints() []Hint
	// SetSize gives the panel its content area, borders already subtracted.
	SetSize(width, height int)
	// SetFocus says whether the keys are currently talking to this panel.
	SetFocus(focused bool)
	View() string
	Update(msg tea.Msg) tea.Cmd
}

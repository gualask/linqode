// Package panel is the chrome a focusable region of the screen wears: a
// bordered box with a title in its top rule, and the footer hints that say
// what the region answers to.
//
// The border is not decoration, and it is not free: two columns and two rows
// per box is the same cost that got the right-hand sidebar removed (September
// 2026). It earns them by naming which region the keys are talking to, so
// every region that takes focus draws one — the header included, where the box
// costs nothing because the title line and the rule below it were paying for
// those two rows already.
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

// statusOverhead is what a right-hand status costs beyond its own text: the
// space before it, the space after it, and one dash between it and the corner.
const statusOverhead = 3

// Box renders content inside a titled border, exactly width by height cells.
// Content is given width-2 by height-2: lines longer than that are truncated
// and lines past the last row are dropped, so a panel can never push its
// neighbours out of place by rendering more than it was allotted.
//
// status is an optional second label, set into the right end of the same rule,
// for what the region is currently showing — the project's service counts, on
// the panel that holds them. It is rendered exactly as given: unlike the
// title, which takes the focus accent, a status carries its own colours,
// because what it says is read by colour and a style laid over it would end at
// its first reset. It is dropped before the title is when the rule is short.
//
// A non-positive size means the caller does not know the terminal's yet; the
// content is returned as it is, the way the status view leaves its lines
// unclipped until the first WindowSizeMsg.
func Box(title, status, content string, focused bool, width, height int) string {
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
	lines = append(lines, topRule(title, status, focused, width))
	side := edge.Render(border.Left)
	for _, line := range blockLines(content, width-2, height-2) {
		lines = append(lines, side+line+side)
	}
	lines = append(lines, edge.Render(border.BottomLeft+
		strings.Repeat(border.Bottom, width-2)+border.BottomRight))
	return strings.Join(lines, "\n")
}

// topRule is the border's top line with the title set into it, and the status
// set into its right end when there is room for both. A title that does not
// fit is truncated, and one that cannot fit at all leaves a plain rule: a box
// with no name still reads as a box.
func topRule(title, status string, focused bool, width int) string {
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

	// The status goes only where the rule can hold it whole. Truncating it
	// would be worse than dropping it: half of "1 unhealthy" is a number
	// beside a word that no longer says which state it counts.
	tail := ""
	if status != "" {
		cost := lipgloss.Width(status) + statusOverhead
		if fill-cost >= 1 {
			tail = " " + status + edge.Render(" "+border.Top)
			fill -= cost
		}
	}
	return edge.Render(border.TopLeft+border.Top+" ") + label.Render(title) +
		edge.Render(" "+strings.Repeat(border.Top, fill)) + tail +
		edge.Render(border.TopRight)
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

// divider separates what works anywhere from what the region with focus
// answers to. A rule rather than another middle dot, because the two sides are
// different kinds of thing and a dot would read as one list of ten.
const divider = "  │  "

// hintSeparator is what stands between two hints on the same half of the line.
const hintSeparator = " · "

// Footer renders the whole hint line: what works wherever you are, a divider,
// then what the focused region is showing and the keys it answers to.
//
// The split is the point. Before it, ten keys ran together in one row and an
// operator reading it had no way to tell which of them would still work after
// pressing `tab`. Now the left half is the same on every screen — it is worth
// learning once — and only the right half changes under you.
//
// status is the focused region's own line ("6 services", "host  8 cores"), and
// is never dropped: it is what the screen is showing rather than a key, and a
// footer that gave up its errors to fit another hint would be trading the
// wrong thing. Hints are dropped by Drop across both sides at once, so the
// two compete on how much they are worth rather than on which side they sit.
func Footer(global []Hint, status string, focused []Hint, width int) string {
	// How few hints the line may be reduced to. Zero where there is a status,
	// because that is then the one thing worth keeping: a key can be
	// rediscovered, and on a bad refresh the status is the error. One where
	// there is not, since a footer of nothing says less than a footer of one
	// thing.
	minimum := 0
	if status == "" {
		minimum = 1
	}
	for {
		// The words of a hint are dim on both sides and its key is not; the
		// status is neither. It is the one thing on this line that is a
		// reading rather than a way to press something, and on a bad refresh
		// it is an error, so it keeps the colour it arrived in.
		left, right := MarkKeys(joinHintText(global), false),
			MarkKeys(joinHintText(focused), true)
		if status != "" && right != "" {
			right = status + theme.Dim.Render("  ·  ") + right
		} else if status != "" {
			right = status
		}

		var line string
		switch {
		case left != "" && right != "":
			line = left + theme.Dim.Render(divider) + right
		case left != "":
			line = left
		default:
			line = right
		}
		if width <= 0 || lipgloss.Width(line) <= width {
			return line
		}
		// Nothing left to give up. What remains is cut to the terminal rather
		// than allowed to wrap, which would push every row above it up by
		// one — the same rule the header lines follow.
		if len(global)+len(focused) <= minimum {
			return lipgloss.NewStyle().MaxWidth(width).Render(line)
		}
		global, focused = dropWorst(global, focused)
	}
}

// dropWorst removes the single most expendable hint from either side. The
// highest Drop goes first, and a tie goes to the focused side, which changes
// under the operator anyway and is therefore the half worth learning less.
func dropWorst(global, focused []Hint) ([]Hint, []Hint) {
	worstGlobal, worstFocused := -1, -1
	for index, hint := range global {
		if worstGlobal < 0 || hint.Drop > global[worstGlobal].Drop {
			worstGlobal = index
		}
	}
	for index, hint := range focused {
		if worstFocused < 0 || hint.Drop >= focused[worstFocused].Drop {
			worstFocused = index
		}
	}
	switch {
	case worstFocused >= 0 && (worstGlobal < 0 ||
		focused[worstFocused].Drop >= global[worstGlobal].Drop):
		return global, slices.Delete(slices.Clone(focused), worstFocused, worstFocused+1)
	case worstGlobal >= 0:
		return slices.Delete(slices.Clone(global), worstGlobal, worstGlobal+1), focused
	default:
		return global, focused
	}
}

func joinHintText(hints []Hint) string {
	texts := make([]string, len(hints))
	for index, hint := range hints {
		texts[index] = hint.Text
	}
	return strings.Join(texts, hintSeparator)
}

// MarkKeys renders a joined hint list — `enter logs · c actions` — with the
// key set apart from what it does: the character to press takes a colour, the
// word stays recessive.
//
// It is what makes a change on this line visible at all. Before it both
// halves were one flat grey, so `tab` swapped three phrases inside a row of
// nine and nothing said which; now the pattern of marked characters differs
// per region and the difference registers before anything has been read. The
// focused half takes the accent that already means "you are here" on a border
// and a title, which is what lets the eye connect the lit panel at the top to
// its keys at the bottom.
//
// The key is the first word of a hint, which every hint in the application is
// shaped around. A hint whose first word is not a key must not be passed
// here: the log view's prompts say `empty clears`, and marking `empty` would
// be advertising a key nobody can press.
func MarkKeys(text string, focused bool) string {
	if text == "" {
		return ""
	}
	key := theme.KeyIdle
	if focused {
		key = theme.KeyFocus
	}
	parts := strings.Split(text, hintSeparator)
	for index, part := range parts {
		name, rest, found := strings.Cut(part, " ")
		if !found {
			parts[index] = key.Render(part)
			continue
		}
		parts[index] = key.Render(name) + theme.Dim.Render(" "+rest)
	}
	return strings.Join(parts, theme.Dim.Render(hintSeparator))
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
	// Status is the panel's half of the footer, and for most panels it is
	// empty: what a region is *showing* goes on its own rule, where it is
	// legible without focus. What is left for here is what went wrong while
	// showing it, which has nowhere else to go and should be absent when
	// there is nothing to report.
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

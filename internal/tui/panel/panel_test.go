package panel

// Tests for the panel chrome. The invariant worth protecting is the size
// contract: a box occupies exactly the cells the layout gave it, whatever its
// content does, because a box that renders one line too many pushes every
// panel below it down a row.
//
// Focus is asserted on the theme tokens rather than on rendered output. These
// tests run without a TTY, where lipgloss emits no color at all (see
// docs/tests.md), so a focused box and an idle one are the same bytes here —
// which is exactly the class of defect the color viewer exists to catch.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/theme"
)

func lines(rendered string) []string { return strings.Split(rendered, "\n") }

func TestBoxOccupiesExactlyItsSize(t *testing.T) {
	cases := []struct {
		name          string
		title         string
		content       string
		width, height int
	}{
		{"empty", "services", "", 40, 6},
		{"short content", "services", "one\ntwo", 40, 6},
		{"more lines than rows", "services", strings.Repeat("row\n", 20), 40, 6},
		{"line wider than the box", "services", strings.Repeat("wide ", 40), 40, 6},
		{"no title", "", "one", 40, 6},
		{"smallest box", "x", "y", 3, 3},
		{"tall and narrow", "x", "y", 8, 20},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, focused := range []bool{false, true} {
				got := lines(Box(test.title, "", test.content, focused, test.width, test.height))
				if len(got) != test.height {
					t.Fatalf("focused=%v: %d lines, want %d", focused, len(got), test.height)
				}
				for index, line := range got {
					if width := lipgloss.Width(line); width != test.width {
						t.Errorf("focused=%v: line %d is %d cells, want %d: %q",
							focused, index, width, test.width, line)
					}
				}
			}
		})
	}
}

func TestBoxCarriesItsTitle(t *testing.T) {
	top := lines(Box("services", "", "row", false, 40, 5))[0]
	if !strings.Contains(top, "services") {
		t.Fatalf("title missing from the top rule: %q", top)
	}
	if !strings.HasPrefix(top, "┌") || !strings.HasSuffix(top, "┐") {
		t.Fatalf("top rule lost its corners: %q", top)
	}
}

func TestBoxTruncatesATitleItCannotFit(t *testing.T) {
	const width = 16
	top := lines(Box("a very long panel title", "", "row", false, width, 5))[0]
	if got := lipgloss.Width(top); got != width {
		t.Fatalf("top rule is %d cells, want %d: %q", got, width, top)
	}
	if !strings.Contains(top, "a very") {
		t.Fatalf("truncated title lost its beginning: %q", top)
	}
	if strings.Contains(top, "title") {
		t.Fatalf("title was not truncated: %q", top)
	}
}

// A box too narrow to hold "┌─ x ─┐" keeps the border and drops the name: the
// alternative is a rule that is all title and no box.
func TestBoxDropsATitleWithNoRoomAtAll(t *testing.T) {
	top := lines(Box("services", "", "row", false, 6, 4))[0]
	if strings.ContainsAny(top, "servi") {
		t.Fatalf("title survived a box too narrow for it: %q", top)
	}
	if top != "┌────┐" {
		t.Fatalf("plain rule = %q, want a full-width rule", top)
	}
}

// Below three cells on either axis a border would be the whole box, so the
// content gets the space instead.
func TestBoxTooSmallForABorderDrawsNone(t *testing.T) {
	got := Box("services", "", "ab", false, 2, 2)
	for _, line := range lines(got) {
		if strings.ContainsAny(line, "┌┐└┘│─") {
			t.Fatalf("border drawn in a 2x2 box: %q", got)
		}
	}
	if len(lines(got)) != 2 {
		t.Fatalf("%d lines, want 2: %q", len(lines(got)), got)
	}
}

func TestBoxLeavesContentAloneAtUnknownSize(t *testing.T) {
	if got := Box("services", "", "row", false, 0, 0); got != "row" {
		t.Fatalf("Box at unknown size = %q, want the content unchanged", got)
	}
}

// Focus is color, and color is what a TTY-less test cannot see. Assert the
// vocabulary instead: whatever the tokens become, the focused ones must not
// collapse onto the idle ones, and neither may reach for Reverse, which marks
// the selected row inside a panel.
func TestFocusTokensAreDistinctFromIdleOnes(t *testing.T) {
	if theme.BorderFocus.GetForeground() == theme.BorderIdle.GetForeground() {
		t.Error("a focused border is indistinguishable from an idle one")
	}
	if theme.TitleFocus.GetForeground() == theme.TitleIdle.GetForeground() {
		t.Error("a focused title is indistinguishable from an idle one")
	}
	if theme.BorderFocus.GetReverse() || theme.TitleFocus.GetReverse() {
		t.Error("focus reached for reverse video, which already means selected row")
	}
	if theme.SelectedIdle.GetReverse() {
		t.Error("the unfocused selection is as loud as the focused one")
	}
	if theme.SelectedIdle.GetBackground() == theme.Reverse.GetBackground() {
		t.Error("the unfocused selection did not quiet down")
	}
}

func TestJoinHintsKeepsEverythingThatFits(t *testing.T) {
	hints := []Hint{{"enter open", 2}, {"r refresh", 1}, {"q quit", 0}}
	got := JoinHints(hints, 80)
	if got != "enter open · r refresh · q quit" {
		t.Fatalf("JoinHints = %q, want every hint", got)
	}
	if unknown := JoinHints(hints, 0); unknown != got {
		t.Fatalf("JoinHints at unknown width = %q, want %q", unknown, got)
	}
}

func TestJoinHintsSacrificesTheMostExpendableFirst(t *testing.T) {
	hints := []Hint{{"enter open", 2}, {"r refresh", 1}, {"q quit", 0}}
	got := JoinHints(hints, 24)
	if strings.Contains(got, "enter open") {
		t.Fatalf("JoinHints = %q, want the highest Drop gone first", got)
	}
	if !strings.Contains(got, "q quit") || !strings.Contains(got, "r refresh") {
		t.Fatalf("JoinHints = %q, want the cheaper hints kept", got)
	}
	if width := lipgloss.Width(got); width > 24 {
		t.Fatalf("JoinHints is %d cells, want at most 24: %q", width, got)
	}
}

// A footer of nothing says less than a footer of one thing, so the last hint
// survives a width it does not fit.
func TestJoinHintsNeverDropsTheLastHint(t *testing.T) {
	got := JoinHints([]Hint{{"enter open", 9}, {"q quit", 0}}, 3)
	if got != "q quit" {
		t.Fatalf("JoinHints = %q, want the least expendable hint kept", got)
	}
}

func TestJoinHintsLeavesItsInputAlone(t *testing.T) {
	hints := []Hint{{"enter open", 2}, {"r refresh", 1}, {"q quit", 0}}
	JoinHints(hints, 10)
	if len(hints) != 3 {
		t.Fatalf("JoinHints shortened its caller's slice to %d", len(hints))
	}
}

// The status is set into the right end of the same rule, and keeps whatever
// colours the caller gave it: the service counts are read by colour, and a
// style laid over them would end at the first reset.
func TestStatusRidesOnTheRightOfTheRule(t *testing.T) {
	rule := strings.Split(Box("services", "4 running · 1 exited", "rows", false, 60, 4), "\n")[0]
	if !strings.Contains(rule, "services") {
		t.Errorf("the title is gone: %q", rule)
	}
	if !strings.Contains(rule, "4 running · 1 exited") {
		t.Errorf("the status is not on the rule: %q", rule)
	}
	if lipgloss.Width(rule) != 60 {
		t.Errorf("the rule is %d wide, want 60: %q", lipgloss.Width(rule), rule)
	}
	// It sits after the title, not before it.
	if strings.Index(rule, "4 running") < strings.Index(rule, "services") {
		t.Errorf("the status precedes the title: %q", rule)
	}
}

// Dropped whole rather than cut: half of "1 unhealthy" is a number beside a
// word that no longer says which state it counts. The title outlives it,
// because a box with no name reads as an unnamed box and a box with a cut one
// reads as a different box.
func TestANarrowRuleDropsTheStatusAndKeepsTheTitle(t *testing.T) {
	for _, width := range []int{12, 16, 20, 24, 28} {
		rule := strings.Split(Box("services", "4 running · 1 exited", "rows", false, width, 4), "\n")[0]
		if strings.Contains(rule, "running") && !strings.Contains(rule, "4 running · 1 exited") {
			t.Errorf("at %d cells the status was cut: %q", width, rule)
		}
		if lipgloss.Width(rule) != width {
			t.Errorf("at %d cells the rule is %d wide: %q", width, lipgloss.Width(rule), rule)
		}
	}
	// Wide enough for the title, not for both.
	rule := strings.Split(Box("services", "4 running · 1 exited", "rows", false, 20, 4), "\n")[0]
	if !strings.Contains(rule, "services") {
		t.Errorf("the title was dropped before the status: %q", rule)
	}
}

// A box with no status is exactly the box it was before there could be one.
func TestNoStatusIsTheRuleItAlwaysWas(t *testing.T) {
	with := Box("services", "", "rows", false, 40, 4)
	if lipgloss.Width(strings.Split(with, "\n")[0]) != 40 {
		t.Errorf("rule width changed: %q", strings.Split(with, "\n")[0])
	}
	if strings.Contains(with, "  ─") {
		t.Errorf("an empty status left a gap in the rule: %q", strings.Split(with, "\n")[0])
	}
}

// The footer is two halves: what works wherever you are, and what the region
// with focus answers to. The split is the point — before it, ten keys ran
// together in one row and nothing said which would survive a `tab`.
func TestFooterSplitsGlobalFromFocused(t *testing.T) {
	line := Footer(
		[]Hint{{Text: "r refresh", Drop: 2}, {Text: "q quit"}},
		" 6 services",
		[]Hint{{Text: "enter logs", Drop: 2}, {Text: "a live", Drop: 5}},
		120)

	for _, want := range []string{"r refresh", "q quit", "6 services", "enter logs", "a live"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing from the footer: %q", want, line)
		}
	}
	if !strings.Contains(line, "│") {
		t.Errorf("the two halves are not divided: %q", line)
	}
	if strings.Index(line, "q quit") > strings.Index(line, "enter logs") {
		t.Errorf("the global half is not on the left: %q", line)
	}
}

// Hints are dropped by Drop across both halves at once, so the two compete on
// what they are worth rather than on which side they sit. The status is never
// dropped: it is what the screen is showing rather than a key, and on a bad
// refresh it is the error.
func TestFooterDropsAcrossBothHalvesButKeepsTheStatus(t *testing.T) {
	global := []Hint{{Text: "r refresh", Drop: 2}, {Text: "x scripts", Drop: 6},
		{Text: "q quit"}}
	focused := []Hint{{Text: "enter logs", Drop: 2}, {Text: "a live", Drop: 5}}

	for _, width := range []int{20, 30, 40, 50, 70, 100} {
		line := Footer(global, " 6 services", focused, width)
		if lipgloss.Width(line) > width {
			t.Errorf("at %d cells the footer is %d wide: %q", width, lipgloss.Width(line), line)
		}
		if !strings.Contains(line, "6 services") {
			t.Errorf("at %d cells the status was dropped: %q", width, line)
		}
	}
	// The most expendable goes first, wherever it sits.
	wide := Footer(global, "", focused, 60)
	if !strings.Contains(wide, "x scripts") {
		t.Fatalf("nothing was dropped at 60 cells: %q", wide)
	}
	tight := Footer(global, "", focused, 45)
	if strings.Contains(tight, "x scripts") {
		t.Errorf("Drop 6 outlived Drop 5 and Drop 2: %q", tight)
	}
	if !strings.Contains(tight, "q quit") {
		t.Errorf("the way out was dropped first: %q", tight)
	}
}

// One half empty is one half of a line, not a stray divider.
func TestFooterOmitsTheDividerWhenOneHalfIsEmpty(t *testing.T) {
	if line := Footer([]Hint{{Text: "q quit"}}, "", nil, 80); strings.Contains(line, "│") {
		t.Errorf("a divider was drawn with nothing after it: %q", line)
	}
	if line := Footer(nil, " 6 services", nil, 80); strings.Contains(line, "│") {
		t.Errorf("a divider was drawn with nothing before it: %q", line)
	}
}

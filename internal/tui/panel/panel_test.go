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
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

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

// The footer is two halves: what works wherever you are, and what the region
// with focus answers to. The split is the point — before it, ten keys ran
// together in one row and nothing said which would survive a `tab`.
// A hint is a key and a word for what it does, and the line has to spell the
// two differently: an operator scanning it is looking for the character to
// press. The key also has to differ between the halves — the focused half
// carries the accent that marks the lit panel, which is what lets the two ends
// of the screen be connected without reading either.
func TestKeyTokensAreThreeDistinctLevels(t *testing.T) {
	if theme.KeyFocus.GetForeground() == theme.KeyIdle.GetForeground() {
		t.Error("the focused half's keys are indistinguishable from the global ones")
	}
	for name, style := range map[string]lipgloss.Style{
		"global": theme.KeyIdle, "focused": theme.KeyFocus} {
		if style.GetForeground() == theme.Dim.GetForeground() {
			t.Errorf("the %s key is the colour of the words around it", name)
		}
	}
	// Not merely different: the *same* accent the focused border wears.
	if theme.KeyFocus.GetForeground() != theme.BorderFocus.GetForeground() {
		t.Error("the focused key is not the colour that marks the focused panel")
	}
}

// MarkKeys may only change how the line looks. Every width decision on this
// footer — what is dropped, where it is cut — is made on the rendered string,
// so a styling pass that added or moved a single cell would be an arithmetic
// bug wearing a colour.
func TestMarkKeysChangesNothingButTheColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	const text = "enter logs · a live · c actions"
	for _, focused := range []bool{false, true} {
		marked := MarkKeys(text, focused)
		if plain := ansi.Strip(marked); plain != text {
			t.Errorf("focused=%v: the text came back as %q", focused, plain)
		}
		if got, want := lipgloss.Width(marked), lipgloss.Width(text); got != want {
			t.Errorf("focused=%v: %d cells, want %d", focused, got, want)
		}
		key := theme.KeyIdle
		if focused {
			key = theme.KeyFocus
		}
		// The key and its word as one substring, which is the assertion that
		// survives KeyIdle setting no foreground at all: whatever the key is
		// dressed in, the dim run must begin after it rather than around it.
		if want := key.Render("c") + theme.Dim.Render(" actions"); !strings.Contains(marked, want) {
			t.Errorf("focused=%v: the key was not set apart from its word: %q", focused, marked)
		}
	}
	// The global key is the terminal's own text colour rather than one of
	// ours: on a palette its owner chose, body text is what they chose.
	if got := theme.KeyIdle.Render("q"); got != "q" {
		t.Errorf("the global key paints its own foreground: %q", got)
	}
	// A hint that is one word is one key, not one word with no key.
	if marked := MarkKeys("q", true); marked != theme.KeyFocus.Render("q") {
		t.Errorf("a single-word hint was not marked as a key: %q", marked)
	}
	if MarkKeys("", true) != "" {
		t.Error("an empty half rendered something")
	}
}

// The colour is what the width arithmetic is most likely to trip over, and
// the tests that check it run where there is none. This one turns it on.
func TestFooterFitsItsWidthInColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	global := []Hint{{Text: "tab panels", Drop: 7}, {Text: "r refresh", Drop: 2},
		{Text: "x scripts", Drop: 6}, {Text: "q quit"}}
	focused := []Hint{{Text: "enter logs", Drop: 2}, {Text: "c actions", Drop: 3}}
	for _, width := range []int{20, 30, 45, 60, 80, 120} {
		line := Footer(global, " 6 services", focused, width)
		if got := lipgloss.Width(line); got > width {
			t.Errorf("at %d cells the footer is %d wide: %q", width, got, ansi.Strip(line))
		}
	}
}

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

// Package theme owns the visual tokens shared by TUI features.
//
// The colors are named explicitly rather than taken from the terminal's
// ANSI slots. `lipgloss.Color("1")` does not ask for red, it asks for
// *this terminal's* red, and the popular schemes — Solarized, Nord,
// Catppuccin, the Terminal.app presets — deliberately desaturate those
// slots. An interface that leans on state color to be scannable then reads
// as washed out through no fault of its own, and there is nothing it can do
// about it. Naming the colors costs the app its ability to blend into a
// user's scheme; it buys knowing what an operator actually sees.
//
// Every token is adaptive: lipgloss picks the Dark or Light variant from
// the terminal's background, so the same palette stays legible on a white
// one. On a terminal with only sixteen colors lipgloss degrades each value
// to its nearest ANSI slot, which is where this started.
package theme

import "github.com/charmbracelet/lipgloss"

// The palette. Dark-background values are vivid; light-background ones are
// darkened to hold contrast against white, which is why they are not just
// the same hues.
var (
	red     = lipgloss.AdaptiveColor{Light: "#d92626", Dark: "#ff5f56"}
	green   = lipgloss.AdaptiveColor{Light: "#0f9d58", Dark: "#2fe07a"}
	yellow  = lipgloss.AdaptiveColor{Light: "#b8860b", Dark: "#ffd23f"}
	blue    = lipgloss.AdaptiveColor{Light: "#1a73e8", Dark: "#4d9fff"}
	magenta = lipgloss.AdaptiveColor{Light: "#b5179e", Dark: "#e56ce5"}
	cyan    = lipgloss.AdaptiveColor{Light: "#0a7ea4", Dark: "#22d7e8"}

	// grey carries everything recessive. It replaced Faint(true), which is
	// an attribute terminals implement by blending the text toward the
	// background — the literal instruction "wash this out". A quarter of
	// what the status view draws is recessive (bar tracks, the footer
	// hints, the I/O columns, separators), so that attribute set the tone
	// of the whole screen. An explicit grey is dimmer than the body text by
	// a chosen amount instead of by whatever the terminal decides.
	grey = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#7a7f87"}

	// surface is a fill a shade off the terminal's own background, for
	// marking a region without lighting it up.
	surface = lipgloss.AdaptiveColor{Light: "#e3e6ea", Dark: "#33383f"}

	// paper is the text laid over a filled band, where the background is
	// ours rather than the terminal's.
	paper = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#ffffff"}
	ink   = lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"}
)

var (
	Bold    = lipgloss.NewStyle().Bold(true)
	Dim     = lipgloss.NewStyle().Foreground(grey)
	Cyan    = lipgloss.NewStyle().Foreground(cyan)
	Red     = lipgloss.NewStyle().Foreground(red)
	Green   = lipgloss.NewStyle().Foreground(green)
	Yellow  = lipgloss.NewStyle().Foreground(yellow)
	Blue    = lipgloss.NewStyle().Foreground(blue)
	Magenta = lipgloss.NewStyle().Foreground(magenta)
	Reverse = lipgloss.NewStyle().Reverse(true)
	// TableHeader is the heading band of a table: it spans the full width,
	// which is how htop and k9s separate a heading from its rows and what
	// makes a table read as occupying the screen rather than trailing off
	// mid-way. Colored, not just shaded: Reverse marks the selected row as
	// a pale bar, and a grey heading was near enough to it that the two
	// read as the same thing on screen.
	TableHeader = lipgloss.NewStyle().Bold(true).Foreground(paper).Background(blue)
	// Match highlights a search hit, dark on the attention color.
	Match = lipgloss.NewStyle().Foreground(ink).Background(yellow)
)

// Panel chrome. A panel is named by its border and its title, and focus is
// carried by their color: the focused one takes the accent, every other one
// recedes to grey. Reverse is deliberately absent here — it already marks
// the selected row *inside* a panel, and two marks that both mean "here"
// cancel each other out.
var (
	BorderFocus = lipgloss.NewStyle().Foreground(blue)
	BorderIdle  = lipgloss.NewStyle().Foreground(grey)
	TitleFocus  = lipgloss.NewStyle().Bold(true).Foreground(blue)
	TitleIdle   = lipgloss.NewStyle().Foreground(grey)

	// KeyIdle and KeyFocus mark the key inside a hint whose words stay Dim:
	// `c actions` is one character to press and one word saying what it does,
	// and a line that spells both the same way is read as prose rather than
	// as a keymap.
	//
	// Body text on the half that works wherever you are, the focus accent on
	// the half that belongs to the region with focus — so the colour that
	// says "you are here" on a border says it on the keymap too, and the two
	// ends of the screen can be connected without reading either.
	//
	// KeyIdle sets no foreground on purpose. The key of an always-available
	// hint is body text — as bright as what the table draws — and on a
	// terminal whose palette belongs to its owner, "body text" is the colour
	// they already chose, not one of ours that happens to look like it. What
	// makes the key stand out is Dim receding from it, which is the same
	// relationship in a light theme, a dark one and a themed one.
	KeyIdle  = lipgloss.NewStyle()
	KeyFocus = lipgloss.NewStyle().Foreground(blue)

	// SelectedIdle is the selected row of a panel that does not hold focus.
	// Reverse would keep shouting from a panel nobody is acting on, so the
	// row keeps its place with a quiet fill instead: still findable when
	// focus comes back, no longer competing with the panel that has it.
	SelectedIdle = lipgloss.NewStyle().Background(surface)
)

// Usage colors a percentage of something finite — memory, a filesystem, a
// container's share of a CPU. The thresholds are what make a screen full of
// numbers scannable: everything is green until three quarters, and red is
// reserved for the last tenth, so a red reading always means the same thing
// wherever it appears.
func Usage(percent float64) lipgloss.Style {
	switch {
	case percent >= 90:
		return Red
	case percent >= 75:
		return Yellow
	default:
		return Green
	}
}

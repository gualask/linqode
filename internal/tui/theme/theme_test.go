package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// A track with no colour to be drawn in is written in `░`, so a gauge on a
// NO_COLOR terminal still says how far it could have gone; with colour it
// is blank cells on the fill.
func TestTrackFallsBackToShadeWithoutColour(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())

	lipgloss.SetColorProfile(termenv.Ascii)
	if got := Track.Render("   "); got != "░░░" {
		t.Fatalf("ascii track = %q, want %q", got, "░░░")
	}

	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.ANSI256, termenv.TrueColor} {
		lipgloss.SetColorProfile(profile)
		got := Track.Render("   ")
		if strings.Contains(got, "░") || !strings.Contains(got, "   ") {
			t.Fatalf("profile %d: track = %q, want blank cells on a fill", profile, got)
		}
	}
}

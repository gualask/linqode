// Package system is the machine itself: the meter band that sits permanently
// in the header, and the view behind it.
//
// The band is one row and it is always drawn — it is the first thing an
// operator reads, and the only reading on the screen that is about the host
// rather than about a container. It takes focus like a panel does, without a
// border it has no room for: the label carries that instead. `enter` on it
// opens the system view, which is where every later reading lands — pressure,
// per-core CPU, temperatures, GPU, the processes behind them — rather than in
// a new box on the home.
//
// The layout follows htop's meters: the bar carries the percentage and the
// text inside it carries the absolute amounts. Printing the percentage as
// well would say twice what the bar already says, and it would cost the
// width that makes the bar worth drawing — at 80 columns that difference is
// a seven-cell bar against a four-cell one.
//
// This replaced a right-hand sidebar (removed September 2026). The sidebar
// spent 30 columns on every row of the screen to show four readings that
// fit on one line, and on a short terminal it truncated its own last
// entries. A header band is also what every system monitor with a wide
// table does — htop, top, glances, k9s — while a sidebar is the convention
// for navigation panels the operator interacts with.

package system

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/tui/theme"
)

// meterMinBar is the narrowest bar still worth drawing. Below this the
// gauge says nothing the number does not, but it still costs the width.
const meterMinBar = 4

// meterMaxBar caps the other end. A bar is read as a proportion, and past
// about thirty cells the extra ones add resolution nobody uses while making
// the band sprawl across a wide terminal — three gauges of forty-odd cells
// each is a lot of screen spent saying "half full".
const meterMaxBar = 30

// bandLabel says whose resources these are. htop and its kin label only the
// individual meters, and so did an earlier draft of this band — but they
// only ever show one machine's CPU and memory. This screen shows CPU and
// MEM twice, once here for the host and once per container in the table,
// and without the word the band reads just as easily as an aggregate of the
// rows below it. The sidebar this replaced carried the same label.
const bandLabel = "host"

// meter is one resource's gauge: a bar to fill and the amounts to print
// inside it.
type meter struct {
	label   string
	value   string
	style   lipgloss.Style
	percent float64
}

// hostMeters is one meter per resource the host reported. A reading it did
// not report is dropped entirely rather than drawn empty.
func (m *Model) meters() []meter {
	metrics := m.metrics
	var meters []meter
	if metrics.HasLoad() {
		// Load is not a percentage of anything, but per-CPU load is: 1.0 is
		// a full machine. That normalization is what the bar shows, which
		// leaves the raw one-minute figure for the text.
		perCPU := metrics.LoadPerCPU()
		meters = append(meters, meter{"load", fmt.Sprintf("%.2f", metrics.Load1),
			loadStyle(perCPU), perCPU * 100})
	}
	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		meters = append(meters, meter{"mem",
			formatKB(metrics.MemUsedKB()) + "/" + formatKB(metrics.MemTotalKB),
			theme.Usage(percent), percent})
	}
	if metrics.DiskTotalKB > 0 {
		percent := metrics.DiskUsedPercent()
		meters = append(meters, meter{"disk",
			formatKB(metrics.DiskUsedKB) + "/" + formatKB(metrics.DiskTotalKB),
			theme.Usage(percent), percent})
	}
	return meters
}

// Band is the header line, drawn to the width the screen gives it. It is
// empty until the first sample, so the header does not reserve a row for
// numbers that are not there yet.
func (m *Model) Band(width int) string { return m.renderBand(width) }

// HasBand reports whether there is a sample to draw, so the screen knows
// whether to reserve the row.
func (m *Model) HasBand() bool { return m.loaded && len(m.meters()) > 0 }

func (m *Model) renderBand(width int) string {
	if !m.loaded {
		return ""
	}
	meters := m.meters()
	if len(meters) == 0 {
		return ""
	}

	uptime := ""
	if m.metrics.Uptime > 0 {
		uptime = "up " + formatUptime(m.metrics.Uptime)
	}
	tail := func() string {
		parts := make([]string, 0, 2)
		if uptime != "" {
			parts = append(parts, uptime)
		}
		if m.stale {
			parts = append(parts, "(stale)")
		}
		return strings.Join(parts, "  ")
	}

	// fixed is everything the bars do not occupy: the leading marker,
	// `label[` and ` value]` per meter, two spaces between meters, and the
	// tail.
	fixed := func() int {
		width := 1 + len(bandLabel) + 2 + 2*(len(meters)-1)
		for _, gauge := range meters {
			width += len(gauge.label) + len(gauge.value) + 3
		}
		if text := tail(); text != "" {
			width += 2 + len(text)
		}
		return width
	}
	// Shed the least urgent parts until the bars can have their minimum,
	// rather than letting the band overflow and wrap the header. Uptime
	// goes first, then the meters from the bottom up — load is the headline
	// reading. The staleness flag stays while anything is drawn at all: a
	// stale number that looks current is worse than a missing one.
	available := width
	for available > 0 && fixed()+meterMinBar*len(meters) > available {
		switch {
		case uptime != "":
			uptime = ""
		case len(meters) > 1:
			meters = meters[:len(meters)-1]
		default:
			// Narrower than a single meter can be drawn; give up on fitting
			// and let the caller's clip deal with it.
			available = 0
		}
	}

	barWidth := meterMinBar
	if available > 0 {
		barWidth = min(max((available-fixed())/len(meters), meterMinBar), meterMaxBar)
	}
	parts := make([]string, len(meters))
	for index, gauge := range meters {
		parts[index] = fmt.Sprintf("%s[%s %s]", gauge.label,
			styledBar(gauge.percent, barWidth, gauge.style), gauge.value)
	}
	line := " " + m.labelStyle().Render(bandLabel) + "  " + strings.Join(parts, "  ")
	if text := tail(); text != "" {
		line += "  " + theme.Dim.Render(text)
	}
	return line
}

// bar renders a percentage as a filled block gauge, clamped at both ends so
// an over-100% reading (load above one per core) stays a full bar rather
// than overflowing its meter.
func bar(percent float64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := barFill(percent, width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// barFill is how many cells of a bar of this width are filled.
func barFill(percent float64, width int) int {
	filled := int(percent/100*float64(width) + 0.5)
	return min(max(filled, 0), width)
}

// styledBar colors only the filled part. Rendering the whole bar in the
// reading's color turned the empty track into a field of bright speckle —
// the `░` cells took the same saturated yellow or red as the fill — so the
// part that means "unused" shouted as loudly as the part that means "used".
func styledBar(percent float64, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	filled := barFill(percent, width)
	return style.Render(strings.Repeat("█", filled)) +
		theme.Dim.Render(strings.Repeat("░", width-filled))
}

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
// text beside it carries the absolute amounts. Printing the percentage as
// well would say twice what the bar already says, and it would cost the
// width that makes the bar worth drawing — at 80 columns that difference is
// a seven-cell bar against a four-cell one. The brackets htop writes around
// its meters are gone with them: they delimit a track of empty space, and
// this one is a fill that delimits itself.
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

	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

// bandGap separates one meter from the next, and the last of them from the
// tail. It is wider than the single space inside a meter, which is what
// binds a label and an amount to the bar between them now that no bracket
// does: `cpu ███▁▁ 14%` is one reading because its parts are closer to each
// other than to anything else on the row.
const bandGap = 3

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

// swapWorthAMeter is the share of swap in use below which the band says
// nothing about it. Almost every Linux machine has a few megabytes swapped
// out and is perfectly healthy; a machine that is *filling* swap is in
// trouble that MemAvailable does not show. So the meter appears exactly
// when it means something, and costs the other three no width until then.
const swapWorthAMeter = 5

// meters is one meter per resource the host reported, in the order they are
// shed: the band drops from the bottom when it runs out of width, so the
// least urgent reading is last.
//
// A reading the host did not report is dropped entirely rather than drawn
// empty.
func (m *Model) meters() []meter {
	metrics := m.metrics
	var meters []meter
	switch {
	case m.hasUsage:
		// What the machine is doing right now, which is a difference
		// between two samples and so cannot exist before the second one.
		percent := m.usage.CPUPercent
		meters = append(meters, meter{"cpu", fmt.Sprintf("%.0f%%", percent),
			theme.Usage(percent), percent})
	case metrics.HasLoad():
		// Until then, the load average. It is not a percentage of anything,
		// but per-CPU load is: 1.0 is a full machine. That normalization is
		// what the bar shows, which leaves the raw one-minute figure for
		// the text.
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
	// The fullest filesystem, not always the root: a comfortable / says
	// nothing about the /var/lib/docker that is about to stop the
	// deployment, and the band has room for one disk meter. Its mount point
	// is the label, so which one it is showing is never in doubt.
	if fullest, ok := metrics.Fullest(); ok {
		percent := fullest.UsedPercent()
		meters = append(meters, meter{fullest.Mount,
			formatKB(fullest.UsedKB) + "/" + formatKB(fullest.TotalKB),
			theme.Usage(percent), percent})
	}
	if percent := metrics.SwapUsedPercent(); percent >= swapWorthAMeter {
		meters = append(meters, meter{"swap",
			formatKB(metrics.SwapUsedKB()) + "/" + formatKB(metrics.SwapTotalKB),
			theme.Usage(percent), percent})
	}
	return meters
}

// Band is the header line, drawn to the width the screen gives it. It is
// empty until the first sample, so the header does not reserve a row for
// numbers that are not there yet.
func (m *Model) Band(width int) string {
	if band := m.renderBand(width); band != "" {
		return band
	}
	// The header is a box from the first frame, before there is anything to
	// put in it: a row that appeared on the first sample would push the whole
	// body down one line a second after the screen opened.
	return theme.Dim.Render(" host  (waiting for the first sample…)")
}

// HasBand reports whether there is a sample behind the band — which is not
// whether there is a band, since the header draws one either way. It is what
// says whether there is a system view worth opening.
func (m *Model) HasBand() bool { return m.loaded && len(m.meters()) > 0 }

func (m *Model) renderBand(width int) string {
	if !m.loaded {
		return ""
	}
	meters := m.meters()
	if len(meters) == 0 {
		return ""
	}

	// The tail is what follows the meters: a temperature, how long the
	// machine has been up, and whether any of it is still current. Each part
	// is measured plain and rendered styled, because the width arithmetic
	// below cannot see through an escape sequence.
	temperature := ""
	temperatureStyle := theme.Dim
	if hottest, ok := m.metrics.Hottest(); ok {
		// A number, not a meter: the band has no width for a fifth gauge,
		// and the colour carries the judgement a bare temperature cannot
		// make for itself. The meter is in the view behind it.
		temperature = fmt.Sprintf("%.0f°C", hottest.Celsius())
		temperatureStyle = theme.Usage(hottest.Share())
	}
	uptime := ""
	if m.metrics.Uptime > 0 {
		uptime = "up " + formatUptime(m.metrics.Uptime)
	}
	tailParts := func() []string {
		parts := make([]string, 0, 3)
		if temperature != "" {
			parts = append(parts, temperature)
		}
		if uptime != "" {
			parts = append(parts, uptime)
		}
		if m.stale {
			parts = append(parts, "(stale)")
		}
		return parts
	}
	tail := func() string { return strings.Join(tailParts(), "  ") }

	// fixed is everything the bars do not occupy: the leading marker, the
	// label before each bar and the amount after it, the gaps between the
	// meters, and the tail.
	fixed := func() int {
		width := 1 + len(bandLabel) + 2 + bandGap*(len(meters)-1)
		for _, gauge := range meters {
			width += len(gauge.label) + len(gauge.value) + 2
		}
		if text := tail(); text != "" {
			width += bandGap + len(text)
		}
		return width
	}
	// Shed the least urgent parts until the bars can have their minimum,
	// rather than letting the band overflow and wrap the header. Uptime goes
	// first, then the temperature, then the meters from the bottom up — CPU
	// is the headline reading and swap the one that is only there when it
	// matters. The staleness flag stays while anything is drawn at all: a
	// stale number that looks current is worse than a missing one.
	available := width
	for available > 0 && fixed()+meterMinBar*len(meters) > available {
		switch {
		case uptime != "":
			uptime = ""
		case temperature != "":
			temperature = ""
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
	gap := strings.Repeat(" ", bandGap)
	parts := make([]string, len(meters))
	for index, gauge := range meters {
		parts[index] = fmt.Sprintf("%s %s %s", gauge.label,
			styledBar(gauge.percent, barWidth, gauge.style), gauge.value)
	}
	line := " " + m.labelStyle().Render(bandLabel) + "  " + strings.Join(parts, gap)
	styled := make([]string, 0, 3)
	for _, part := range tailParts() {
		if part == temperature {
			styled = append(styled, temperatureStyle.Render(part))
			continue
		}
		styled = append(styled, theme.Dim.Render(part))
	}
	if len(styled) > 0 {
		line += gap + strings.Join(styled, theme.Dim.Render("  "))
	}
	return line
}

// styledBar is the gauge every meter here is drawn with — the band's and the
// view's, which is why it is one function: see spark.Meter for why only its
// filled part takes the reading's colour.
func styledBar(percent float64, width int, style lipgloss.Style) string {
	return spark.Meter(percent, width, style, theme.Track)
}

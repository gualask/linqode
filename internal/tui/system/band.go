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

// bandPathFloor is the shortest a mount point is cut to on the band before a
// meter is dropped instead: `…/docker` still says which disk it is, and a
// label shorter than that says nothing the amount beside it does not.
const bandPathFloor = 8

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
	tail := m.bandTail()
	meters, available := fitBand(meters, &tail, width)

	barWidth := meterMinBar
	if available > 0 {
		barWidth = min(max((available-bandFixed(meters, tail))/len(meters), meterMinBar), meterMaxBar)
	}
	gap := strings.Repeat(" ", bandGap)
	parts := make([]string, len(meters))
	for index, gauge := range meters {
		parts[index] = fmt.Sprintf("%s %s %s", gauge.label,
			styledBar(gauge.percent, barWidth, gauge.style), gauge.value)
	}
	line := " " + m.labelStyle().Render(bandLabel) + "  " + strings.Join(parts, gap)
	if styled := tail.render(); styled != "" {
		line += gap + styled
	}
	return line
}

// bandTail is what follows the meters: a temperature, how long the machine
// has been up, and whether any of it is still current. Each part is measured
// plain and rendered styled, because the width arithmetic cannot see through
// an escape sequence.
type bandTail struct {
	temperature      string
	temperatureStyle lipgloss.Style
	uptime           string
	stale            bool
}

func (m *Model) bandTail() bandTail {
	tail := bandTail{temperatureStyle: theme.Dim, stale: m.stale}
	if hottest, ok := m.metrics.Hottest(); ok {
		// A number, not a meter: the band has no width for a fifth gauge,
		// and the colour carries the judgement a bare temperature cannot
		// make for itself. The meter is in the view behind it.
		tail.temperature = fmt.Sprintf("%.0f°C", hottest.Celsius())
		tail.temperatureStyle = theme.Usage(hottest.Share())
	}
	if m.metrics.Uptime > 0 {
		tail.uptime = "up " + formatUptime(m.metrics.Uptime)
	}
	return tail
}

// parts is the tail's pieces still in it, in the order they are drawn.
func (t bandTail) parts() []string {
	parts := make([]string, 0, 3)
	if t.temperature != "" {
		parts = append(parts, t.temperature)
	}
	if t.uptime != "" {
		parts = append(parts, t.uptime)
	}
	if t.stale {
		parts = append(parts, "(stale)")
	}
	return parts
}

// plain is the tail as the width arithmetic measures it.
func (t bandTail) plain() string { return strings.Join(t.parts(), "  ") }

// render is the tail as drawn: the temperature in the colour of its reading,
// the rest recessive.
func (t bandTail) render() string {
	styled := make([]string, 0, 3)
	for _, part := range t.parts() {
		if part == t.temperature {
			styled = append(styled, t.temperatureStyle.Render(part))
			continue
		}
		styled = append(styled, theme.Dim.Render(part))
	}
	return strings.Join(styled, theme.Dim.Render("  "))
}

// bandFixed is everything on the band the bars do not occupy: the leading
// marker, the label before each bar and the amount after it, the gaps between
// the meters, and the tail.
//
// It counts cells rather than bytes: a mount point can be in any script, and
// the temperature's degree sign alone is two bytes and one cell.
func bandFixed(meters []meter, tail bandTail) int {
	width := 1 + len(bandLabel) + 2 + bandGap*(len(meters)-1)
	for _, gauge := range meters {
		width += lipgloss.Width(gauge.label) + lipgloss.Width(gauge.value) + 2
	}
	if text := tail.plain(); text != "" {
		width += bandGap + lipgloss.Width(text)
	}
	return width
}

// fitBand sheds the least urgent parts until the bars can have their minimum,
// rather than letting the band overflow and wrap the header. Uptime goes
// first, then the temperature, then the head of the disk's mount point, then
// the meters from the bottom up — CPU is the headline reading and swap the
// one that is only there when it matters. The staleness flag stays while
// anything is drawn at all: a stale number that looks current is worse than a
// missing one.
//
// The mount point is cut before any meter goes because the disk meter follows
// the fullest filesystem, which is the reading most likely to be why the
// session was opened, and a long path was dropping it whole at eighty columns.
//
// It returns the meters left and the width to lay them out in, which is zero
// when the terminal is narrower than a single meter can be drawn: fitting is
// given up on, and the caller's clip deals with it. A width not known yet is
// returned as it came, with everything in.
func fitBand(meters []meter, tail *bandTail, width int) ([]meter, int) {
	if width <= 0 {
		// No size yet: nothing to fit to, so nothing is shed.
		return meters, width
	}
	for bandFixed(meters, *tail)+meterMinBar*len(meters) > width {
		switch {
		case tail.uptime != "":
			tail.uptime = ""
		case tail.temperature != "":
			tail.temperature = ""
		case shortenPath(meters, bandFixed(meters, *tail)+meterMinBar*len(meters)-width):
		case len(meters) > 1:
			meters = meters[:len(meters)-1]
		default:
			return meters, 0
		}
	}
	return meters, width
}

// shortenPath cuts the head off the first meter label that is a path longer
// than bandPathFloor, by the cells the band is over or down to the floor,
// whichever is less. It reports whether there was one to cut.
func shortenPath(meters []meter, over int) bool {
	for index, gauge := range meters {
		width := lipgloss.Width(gauge.label)
		if !strings.HasPrefix(gauge.label, "/") || width <= bandPathFloor {
			continue
		}
		meters[index].label = trimPath(gauge.label, max(width-over, bandPathFloor))
		return true
	}
	return false
}

// styledBar is the gauge every meter here is drawn with — the band's and the
// view's, which is why it is one function: see spark.Meter for why only its
// filled part takes the reading's colour.
func styledBar(percent float64, width int, style lipgloss.Style) string {
	return spark.Meter(percent, width, style, theme.Track)
}

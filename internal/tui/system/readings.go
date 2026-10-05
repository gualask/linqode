package system

// The machine's state: what the band shows in one row, and what the view
// behind it shows when there is a screen to spend.
//
// The model holds a sample rather than fetching one. Who samples, and how
// often, is the screen's business — the same sample feeds the band and the
// view, and a panel that owned its own timer would be one more thing to keep
// in step with the rest of the refresh.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

// The machine's readings in the system view: a row per reading, laid out in
// one column or two, each a label, a meter and the text beside it.

// readingColumnMin is the narrowest a column of readings is laid out at: the
// label, a gauge, and the longest text a row carries — the pressure row's.
// Below twice that the readings are one column, as they always were.
const readingColumnMin = 96

// readingLines lays the readings out: side by side in two columns where there
// is room for two, one under the other otherwise. Either way the first
// column is what fills up — processors, memory, disks — and the second what
// the machine is doing and what it is, so read top to bottom the single
// column is the order it always had.
func (m *Model) readingLines() []string {
	label := m.labelColumn()
	if m.width >= 2*readingColumnMin+columnGap {
		width := (m.width - columnGap) / 2
		capacity, activity := m.readingRows(grid{label: label, width: width})
		return besideEachOther(capacity, activity, width)
	}
	capacity, activity := m.readingRows(grid{label: label, width: m.width})
	return append(capacity, activity...)
}

// readingRows is every reading the batch has, as the two halves readingLines
// lays out. Each half is in the order a short terminal keeps: the box
// truncates from the bottom, so the readings that answer "what is wrong with
// this machine" come before the ones that answer "how long has it been up".
func (m *Model) readingRows(g grid) (capacity, activity []string) {
	metrics := m.metrics
	load := ""
	if metrics.HasLoad() {
		load = theme.Dim.Render(fmt.Sprintf("   load %.2f  %.2f  %.2f   over %d cores",
			metrics.Load1, metrics.Load5, metrics.Load15, metrics.CPUs))
	}
	if m.hasUsage {
		percent := m.usage.CPUPercent
		capacity = append(capacity, m.meterRow(g, "cpu", percent, theme.Usage(percent),
			fmt.Sprintf("%.0f%% busy", percent)+load))
	} else if metrics.HasLoad() {
		// The first sample after connecting cannot say what the CPU is
		// doing — a percentage is a difference — so the load average
		// stands in until the second one arrives.
		perCPU := metrics.LoadPerCPU()
		capacity = append(capacity, m.meterRow(g, "load", perCPU*100, loadStyle(perCPU),
			fmt.Sprintf("%.2f  %.2f  %.2f", metrics.Load1, metrics.Load5, metrics.Load15)+
				theme.Dim.Render(fmt.Sprintf("   over %d cores", metrics.CPUs))))
	}
	// Outside that branch, because the row is drawn on the first sample
	// too: a row that arrives with the second one moves every row under
	// it, the charts and both process lists included.
	if strip := m.coreRow(g); strip != "" {
		capacity = append(capacity, strip)
	}
	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		capacity = append(capacity, m.meterRow(g, "memory", percent, theme.Usage(percent),
			fmt.Sprintf("%s used", formatKB(metrics.MemUsedKB()))+
				theme.Dim.Render(fmt.Sprintf("   %s available of %s",
					formatKB(metrics.MemAvailableKB), formatKB(metrics.MemTotalKB)))))
	}
	// A machine with no swap configured is not a machine with empty swap,
	// and drawing an empty meter for it would say the opposite.
	if metrics.SwapTotalKB > 0 {
		percent := metrics.SwapUsedPercent()
		capacity = append(capacity, m.meterRow(g, "swap", percent, theme.Usage(percent),
			fmt.Sprintf("%s used", formatKB(metrics.SwapUsedKB()))+
				theme.Dim.Render(fmt.Sprintf("   %s free of %s",
					formatKB(metrics.SwapTotalKB-metrics.SwapUsedKB()),
					formatKB(metrics.SwapTotalKB)))))
	}
	for _, filesystem := range m.filesystems() {
		percent := filesystem.UsedPercent()
		free := filesystem.TotalKB - min(filesystem.UsedKB, filesystem.TotalKB)
		text := fmt.Sprintf("%s used", formatKB(filesystem.UsedKB)) +
			theme.Dim.Render(fmt.Sprintf("   %s free of %s",
				formatKB(free), formatKB(filesystem.TotalKB)))
		// Before the device, which is the part of this row a narrow
		// terminal can best spare: when the disk will be full is the thing
		// the meter cannot say.
		if eta, ok := m.history.fillTime(filesystem.Mount, filesystem.UsedKB, filesystem.TotalKB); ok {
			text += "   " + fillStyle(eta).Render("full in "+formatETA(eta))
		}
		text += theme.Dim.Render("   " + panel.Plain(filesystem.Device))
		capacity = append(capacity, m.meterRow(g, filesystem.Mount, percent,
			theme.Usage(percent), text))
	}

	// Which interfaces there are is in the sample; what they are carrying is
	// a difference between two, so the row is here from the first one and
	// says plainly that it has no number yet.
	if len(metrics.Interfaces) > 0 {
		text := theme.Dim.Render(unknownReading)
		if m.hasUsage {
			text = m.networkText()
		}
		activity = append(activity, m.textRow(g, "net", text))
	}
	if metrics.HasPressure() {
		activity = append(activity, m.textRow(g, "pressure", m.pressureText()))
	}
	if hottest, ok := metrics.Hottest(); ok {
		// The meter is the hottest sensor's share of its own limit, which is
		// the only honest way to compare two of them: an NVMe at 71 degrees
		// is closer to trouble than a CPU at 80.
		share := hottest.Share()
		activity = append(activity, m.meterRow(g, "temp", share,
			theme.Usage(share), m.temperatureText(hottest)))
	}
	activity = append(activity, m.gpuRows(g)...)
	if metrics.Uptime > 0 {
		activity = append(activity, m.textRow(g, "uptime", formatUptime(metrics.Uptime)))
	}
	// Last, because it is the row that answers no question about what is
	// wrong: the order is by urgency and the box truncates from the bottom,
	// so what the machine calls itself is what should go last.
	if m.os != "" {
		activity = append(activity, m.textRow(g, "system", theme.Dim.Render(panel.Plain(m.os))))
	}
	return capacity, activity
}

// filesystems is the mount list, falling back to the root reading alone on a
// host whose full `df` did not answer — which is what the guard around it
// exists to survive.
func (m *Model) filesystems() []host.Filesystem { return filesystemsOf(m.metrics) }

func filesystemsOf(metrics host.Metrics) []host.Filesystem {
	if len(metrics.Filesystems) > 0 {
		return metrics.Filesystems
	}
	if metrics.DiskTotalKB == 0 {
		return nil
	}
	return []host.Filesystem{{
		Mount: "/", TotalKB: metrics.DiskTotalKB, UsedKB: metrics.DiskUsedKB}}
}

// formatETA writes a fill time the way it deserves to be read, roughly: to
// the minute inside the hour, to the hour beyond it.
func formatETA(eta time.Duration) string {
	if eta < time.Hour {
		return fmt.Sprintf("~%dm", max(int(eta.Round(time.Minute)/time.Minute), 1))
	}
	return fmt.Sprintf("~%dh", int(eta.Round(time.Hour)/time.Hour))
}

// fillStyle colours a fill time: red inside the hour, which is a disk that
// will stop the deployment before anybody has finished investigating, and
// yellow otherwise. There is no green, because a projection is only shown
// when there is something to say.
func fillStyle(eta time.Duration) lipgloss.Style {
	if eta <= time.Hour {
		return theme.Red
	}
	return theme.Yellow
}

// labelColumn is wide enough for every label this render will draw, so the
// gauges of a machine with a /var/lib/postgresql still line up with the
// ones above them.
func (m *Model) labelColumn() int {
	column := labelWidth
	for _, filesystem := range m.filesystems() {
		column = max(column, lipgloss.Width(filesystem.Mount))
	}
	return min(column, labelMax)
}

// meterRow is one reading: its name, its gauge, and the numbers the gauge
// cannot carry. The bar shows the percentage, so no row prints one twice —
// except the CPU, whose reading has no absolute amount to print instead.
//
// The gauge used to be written between brackets, the way htop writes its
// meters. htop needs them: its track is empty space, and without the `]`
// there is nothing to say how far the bar could have gone. This one stands
// in a fill that ends where it ends, and a delimiter around a shape that
// delimits itself is two cells spent saying it twice.
func (m *Model) meterRow(g grid, label string, percent float64, style lipgloss.Style, text string) string {
	return fmt.Sprintf(" %s %s  %s", pad(label, g.label),
		styledBar(percent, g.gauge(), style), text)
}

// textRow is a reading with no meter, aligned so its text starts where the
// gauges' does rather than under their labels. The gauge's room is left
// blank rather than tracked: a track is the shape of a reading that has one,
// and these rows have none.
func (m *Model) textRow(g grid, label, text string) string {
	return fmt.Sprintf(" %s %s  %s", pad(label, g.label),
		strings.Repeat(" ", g.gauge()), text)
}

// coreRow is the cores drawn as heights, tallest for the busiest. It is the
// row that makes the average above it readable: one pinned core among eight
// idle ones is a machine with a problem and an average that says twelve
// percent.
//
// Heights rather than a labelled bar each, because a labelled bar each stops
// fitting somewhere around sixteen cores and the shape of the row is what it
// is read for. The number that matters is called out beside it.
//
// It stands in the gauges' track, as wide as they are. It used to be one
// cell per core on the terminal's own background, which left it the one
// shape in the column with no track — a strip floating where every row above
// and below it has a slab, and eight cells wide where they are thirty.
func (m *Model) coreRow(g grid) string {
	if !m.hasUsage {
		// One sample says how many cores there are and nothing about what any
		// of them is doing. A strip of lowest cells would say they are all
		// idle, which is a reading rather than a blank, so the row draws the
		// empty track and says beside it that it has no numbers yet.
		if len(m.metrics.CPUTimes) < 2 {
			return ""
		}
		return fmt.Sprintf(" %s %s  %s", pad("cores", g.label),
			theme.Track.Render(strings.Repeat(" ", g.gauge())),
			theme.Dim.Render(unknownReading))
	}
	if len(m.usage.Cores) == 0 {
		return ""
	}
	busiest, index := 0.0, 0
	for core, percent := range m.usage.Cores {
		if percent > busiest {
			busiest, index = percent, core
		}
	}
	cells := coreCells(m.usage.Cores, g.gauge())
	var strip strings.Builder
	for first := 0; first < len(cells); {
		if cells[first] == coreGap {
			strip.WriteString(theme.Track.Render(" "))
			first++
			continue
		}
		last := first
		for last < len(cells) && cells[last] != coreGap {
			last++
		}
		run := cells[first:last]
		strip.WriteString(spark.Strip(run, 0, 100,
			func(cell int) lipgloss.Style { return theme.Usage(run[cell]) }, theme.Track))
		first = last
	}
	return fmt.Sprintf(" %s %s  %s", pad("cores", g.label), strip.String(),
		theme.Dim.Render(fmt.Sprintf("busiest cpu%d at %.0f%%", index, busiest)))
}

// coreGap marks a cell of coreCells that is the track between two cores
// rather than a reading.
const coreGap = -1

// coreCells spreads the cores over the cells a gauge is wide. With room to
// spare each core takes several cells, with a cell of track between one
// core and the next: without it two neighbours under the same load are one
// bar twice as wide, and the row stops saying how many cores there are.
// Past that each cell carries the busiest of the cores it covers, which is
// what the row is for — a pinned core among sixty-four is the reading, and
// showing every other core would be the one way to lose it.
func coreCells(cores []float64, width int) []float64 {
	if width <= 0 || len(cores) == 0 {
		return nil
	}
	if gaps := len(cores) - 1; gaps > 0 && width >= len(cores)+gaps {
		cells := make([]float64, 0, width)
		room := width - gaps
		for core, percent := range cores {
			if core > 0 {
				cells = append(cells, coreGap)
			}
			for range (core+1)*room/len(cores) - core*room/len(cores) {
				cells = append(cells, percent)
			}
		}
		return cells
	}
	cells := make([]float64, width)
	for cell := range cells {
		first := cell * len(cores) / width
		last := min(max((cell+1)*len(cores)/width, first+1), len(cores))
		for _, percent := range cores[first:last] {
			cells[cell] = max(cells[cell], percent)
		}
	}
	return cells
}

// networkText is the machine's throughput and the interface carrying most of
// it. Only the interfaces that carry the machine's own traffic are summed —
// a container's bytes cross a veth and a bridge before reaching the one that
// already counted them — but the busiest is named from all of them, since
// naming a veth is exactly the answer when a container is the one talking.
func (m *Model) networkText() string {
	text := fmt.Sprintf("%s down   %s up", formatRate(m.usage.RxRate), formatRate(m.usage.TxRate))
	busiest, rate := "", 0.0
	for _, entry := range m.usage.Interfaces {
		if entry.Name == "lo" {
			// Loopback is never the answer to who is talking.
			continue
		}
		if total := entry.RxRate + entry.TxRate; total > rate {
			busiest, rate = entry.Name, total
		}
	}
	if busiest != "" && rate > 0 {
		text += theme.Dim.Render(fmt.Sprintf("   busiest %s at %s",
			panel.Plain(busiest), formatRate(rate)))
	}
	return text
}

// pressureText is PSI: the share of the last ten seconds in which something
// was stalled waiting for each resource. It is the better answer to "is this
// machine suffering" than the load average two rows up — load counts
// runnable tasks, which says nothing about what they are waiting for.
//
// Only `some` is printed for each resource; `full`, which means nothing ran
// at all, is added where it is not zero, because a machine reporting it is
// in a different kind of trouble.
func (m *Model) pressureText() string {
	pressure := m.metrics.Pressure
	parts := make([]string, 0, 3)
	for _, reading := range []struct {
		label string
		value host.Pressure
	}{
		{"cpu", pressure.CPU}, {"io", pressure.IO}, {"mem", pressure.Memory},
	} {
		part := fmt.Sprintf("%s %s", reading.label,
			pressureStyle(reading.value.Some10).Render(fmt.Sprintf("%.1f%%", reading.value.Some10)))
		if reading.value.Full10 > 0 {
			part += theme.Dim.Render(fmt.Sprintf(" (full %.1f%%)", reading.value.Full10))
		}
		parts = append(parts, part)
	}
	// The legend is short because this row has no meter to give up width:
	// at a hundred columns the three readings and their explanation are
	// already most of a line.
	return strings.Join(parts, "   ") + theme.Dim.Render("   stalled 10s")
}

// temperatureText is the hottest sensor spelled out, and the rest of them
// behind it. What each is called comes from the chip when the chip says —
// `Package id 0`, `Composite` — because "the nvme is at 71" is a sentence and
// "hwmon2 is at 71" is not.
func (m *Model) temperatureText(hottest host.Sensor) string {
	text := fmt.Sprintf("%.0f°C %s", hottest.Celsius(), panel.Plain(hottest.Name()))
	if hottest.HasLimit() {
		text += theme.Dim.Render(fmt.Sprintf(" of %.0f°C",
			float64(hottest.LimitMilliC)/1000))
	} else {
		// The scale is ours, not the chip's, and a number drawn against a
		// made-up denominator should say so.
		text += theme.Dim.Render(" (no limit reported)")
	}
	rest := make([]string, 0, len(m.metrics.Sensors))
	for _, sensor := range m.metrics.Sensors[1:] {
		rest = append(rest, fmt.Sprintf("%.0f°C %s", sensor.Celsius(), panel.Plain(sensor.Name())))
	}
	if len(rest) > 0 {
		text += theme.Dim.Render("   " + strings.Join(rest, "   "))
	}
	return text
}

// pressureStyle colors a stall share. Any sustained pressure at all is worth
// noticing — unlike a usage percentage, where half full is unremarkable — so
// the thresholds sit far lower than the ones on the meters.
func pressureStyle(percent float64) lipgloss.Style {
	switch {
	case percent >= 20:
		return theme.Red
	case percent >= 5:
		return theme.Yellow
	default:
		return theme.Green
	}
}

// trimPath keeps the last column cells of a path, marking the cut with an
// ellipsis. Anything that is not a path — the meter labels, which are one
// short word — is returned untouched and truncated the ordinary way.
func trimPath(label string, column int) string {
	if column < 4 || !strings.HasPrefix(label, "/") || lipgloss.Width(label) <= column {
		return label
	}
	runes := []rune(label)
	// A whole segment at a time where one fits, so the cut lands where a
	// reader expects a path to be abbreviated: `…/Volumes/Data`, not
	// `…em/Volumes/Data`.
	for cut, r := range runes {
		if r != '/' {
			continue
		}
		if trimmed := "…" + string(runes[cut:]); lipgloss.Width(trimmed) <= column {
			return trimmed
		}
	}
	// Otherwise as much of the last segment as fits, walked back from the end
	// by cells: a character is not a cell, and a mount point in a script that
	// takes two per character would otherwise put the cut before the start.
	cut, used := len(runes), lipgloss.Width("…")
	for cut > 0 {
		width := lipgloss.Width(string(runes[cut-1]))
		if used+width > column {
			break
		}
		cut, used = cut-1, used+width
	}
	return "…" + string(runes[cut:])
}

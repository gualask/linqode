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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/theme"
)

type Model struct {
	// metrics is the last good sample. A failed one keeps it on screen and
	// flags it stale, the same way a failed service refresh keeps the last
	// good table: a reading that is a few seconds old still says more than
	// a blank.
	metrics host.Metrics
	loaded  bool
	stale   bool

	// usage is what the last two samples say that neither says alone: CPU
	// percentages and network rates. It arrives one sample late — a
	// difference needs two readings — which is why the band shows load
	// until it does, rather than an empty meter.
	usage    host.Usage
	hasUsage bool

	// history remembers the readings worth a trend. It costs the server
	// nothing: these samples have already been fetched.
	history history

	// diskUsage is what the daemon says it is holding, read only while this
	// view is open and on a much slower clock than anything else: it is the
	// one reading here that is genuinely slow on a real host.
	diskUsage []compose.DiskUsage

	// gpus is what the graphics cards report, on the same gate as the
	// process table: one of the two vendors answers only through a tool that
	// initialises a driver context, which is not a thing to run every five
	// seconds for a screen nobody is looking at.
	gpus []host.GPU

	// The process table, read only while this view is open. previous is what
	// the CPU shares are measured against.
	processes         []host.ProcessUsage
	previousProcesses host.ProcessSample
	processesStale    bool
	ranking           ranking

	// open is whether this is the view on screen rather than the band in the
	// header. The two answer to different keys.
	open bool

	focused       bool
	width, height int

	// os is what the host calls itself, established once by the connect-time
	// probe. It is the one thing here that is not a reading: it does not
	// change, it is not sampled, and it never goes stale.
	os string
	// unavailable is what the probe found this host cannot do. It is a
	// sentence about the machine, which is what this view is a list of, and
	// it goes at the top for the reason the rest of the list is ordered the
	// way it is: the box truncates from the bottom, and this is the row most
	// likely to be the answer to "why is this screen not what I expected".
	unavailable string
}

func New(os, unavailable string) *Model {
	return &Model{os: os, unavailable: unavailable}
}

// SetSample applies one host sample. A failed sample marks what is on screen
// stale instead of clearing it; the error itself is not shown, because the
// staleness is what an operator can act on.
func (m *Model) SetSample(metrics host.Metrics, err error) {
	if err != nil {
		m.stale = true
		return
	}
	if m.loaded {
		if usage, ok := metrics.Since(m.metrics); ok {
			m.usage, m.hasUsage = usage, true
			m.history.push(trend{
				uptime: metrics.UptimeSeconds,
				cpu:    usage.CPUPercent,
				memory: metrics.MemUsedPercent(),
				net:    usage.RxRate + usage.TxRate,
			})
		}
	}
	m.metrics, m.loaded, m.stale = metrics, true, false
}

// Metrics is the last good sample, for a caller that needs a number this
// panel happens to hold — the container readings borrow the machine's memory
// as the ceiling for a container that has no limit of its own.
func (m *Model) Metrics() host.Metrics { return m.metrics }

// Title names the view in its top rule. The band has no rule to put it in.
func (m *Model) Title() string { return "system" }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }
func (m *Model) SetFocus(focused bool)     { m.focused = focused }

// labelStyle draws the band's `host` label, which is where its focus shows:
// the band is one row and has no border to color, and giving it one would
// cost the row a third of its width.
func (m *Model) labelStyle() lipgloss.Style {
	if m.focused {
		return theme.TitleFocus
	}
	return theme.TitleIdle
}

// Hints are the keys this panel answers to, which are not the same in its two
// forms: the band offers the way in, the view offers what can be done once
// inside it.
func (m *Model) Hints() []panel.Hint {
	if m.open {
		next := byCPU
		if m.ranking == byCPU {
			next = byMemory
		}
		return []panel.Hint{{Text: "s " + next.String(), Drop: 5}}
	}
	return []panel.Hint{{Text: "enter system", Drop: 2}}
}

// Status is this panel's half of the footer, and there is nothing for it to
// say. The footer is the keymap; core count and uptime are readings, and both
// are already on screen — uptime in the band's own tail, cores in the view
// this panel opens. Staleness is in the band too, for the same reason.
func (m *Model) Status() string { return "" }

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if m.open && key.String() == "s" {
		m.toggleRanking()
	}
	return nil
}

// detailBar is the width of the view's gauges. Wider than the band's, which
// shares its row with two other meters, but not the whole line: past thirty
// cells a bar adds resolution nobody reads.
const detailBar = 30

// labelWidth aligns the rows' first column. It is a floor, not a limit: a
// mount point longer than this widens the column for every row rather than
// pushing its own bar out of line with the others.
const labelWidth = 8

// labelMax caps that widening. A deeply nested mount point is truncated
// instead of eating the width the gauges are drawn in.
const labelMax = 16

// row is one line of the view: its text, and the trend strip that belongs to
// the right of it when there is room for a column of them.
type row struct {
	text  string
	strip string
}

// View is the system view: everything the batch reads, with the room to
// print what the band has to leave out — the other load averages, the cores
// behind the average, what is available rather than only what is used, and
// the readings the band has no row for at all.
//
// The order is what a short terminal keeps: the box truncates from the
// bottom, so the readings that answer "what is wrong with this machine" come
// before the ones that answer "how long has it been up".
//
// Temperatures, GPU and the processes behind these numbers arrive in the
// phases after this one, as rows here rather than as boxes on the home.
func (m *Model) View() string {
	if !m.loaded {
		return theme.Dim.Render("  (waiting for the first host sample…)")
	}
	metrics := m.metrics
	column := m.labelColumn()
	var rows []row

	if m.unavailable != "" {
		rows = append(rows,
			row{text: "  " + theme.Yellow.Render("compose is unavailable on this host")},
			row{text: "  " + theme.Dim.Render(m.unavailable)},
			row{})
	}

	load := ""
	if metrics.HasLoad() {
		load = theme.Dim.Render(fmt.Sprintf("   load %.2f  %.2f  %.2f   over %d cores",
			metrics.Load1, metrics.Load5, metrics.Load15, metrics.CPUs))
	}
	if m.hasUsage {
		percent := m.usage.CPUPercent
		rows = append(rows, row{
			text: m.meterRow(column, "cpu", percent, theme.Usage(percent),
				fmt.Sprintf("%.0f%% busy", percent)+load),
			strip: m.trendOf(func(t trend) float64 { return t.cpu }, percentScale, theme.Usage),
		})
		if strip := m.coreRow(column); strip != "" {
			rows = append(rows, row{text: strip})
		}
	} else if metrics.HasLoad() {
		// The first sample after connecting cannot say what the CPU is
		// doing — a percentage is a difference — so the load average
		// stands in until the second one arrives.
		perCPU := metrics.LoadPerCPU()
		rows = append(rows, row{text: m.meterRow(column, "load", perCPU*100, loadStyle(perCPU),
			fmt.Sprintf("%.2f  %.2f  %.2f", metrics.Load1, metrics.Load5, metrics.Load15)+
				theme.Dim.Render(fmt.Sprintf("   over %d cores", metrics.CPUs)))})
	}

	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		// Memory is the reading a trend changes most: 88% in use says
		// nothing about whether it has been there for a week or arrived in
		// the last two minutes, and those are different problems.
		rows = append(rows, row{
			text: m.meterRow(column, "memory", percent, theme.Usage(percent),
				fmt.Sprintf("%s used", formatKB(metrics.MemUsedKB()))+
					theme.Dim.Render(fmt.Sprintf("   %s available of %s",
						formatKB(metrics.MemAvailableKB), formatKB(metrics.MemTotalKB)))),
			strip: m.trendOf(func(t trend) float64 { return t.memory }, percentScale, theme.Usage),
		})
	}
	// A machine with no swap configured is not a machine with empty swap,
	// and drawing an empty meter for it would say the opposite.
	if metrics.SwapTotalKB > 0 {
		percent := metrics.SwapUsedPercent()
		rows = append(rows, row{text: m.meterRow(column, "swap", percent, theme.Usage(percent),
			fmt.Sprintf("%s used", formatKB(metrics.SwapUsedKB()))+
				theme.Dim.Render(fmt.Sprintf("   %s free of %s",
					formatKB(metrics.SwapTotalKB-metrics.SwapUsedKB()),
					formatKB(metrics.SwapTotalKB))))})
	}

	for _, filesystem := range m.filesystems() {
		percent := filesystem.UsedPercent()
		free := filesystem.TotalKB - min(filesystem.UsedKB, filesystem.TotalKB)
		rows = append(rows, row{text: m.meterRow(column, filesystem.Mount, percent,
			theme.Usage(percent),
			fmt.Sprintf("%s used", formatKB(filesystem.UsedKB))+
				theme.Dim.Render(fmt.Sprintf("   %s free of %s   %s",
					formatKB(free), formatKB(filesystem.TotalKB), filesystem.Device)))})
	}

	// Under the filesystems, because it is the answer to the question they
	// raise: a /var at 93% says nothing about how much of it is images
	// nobody is running, and only the daemon knows that.
	if len(m.diskUsage) > 0 {
		rows = append(rows, m.dockerUsageRow(column))
	}

	if m.hasUsage && len(m.usage.Interfaces) > 0 {
		// A throughput is not a share of anything, so its strip is scaled
		// against the busiest moment in the window and drawn in one color:
		// coloring it by height would read as "this is bad" where it only
		// means "this is the top of what happened".
		rows = append(rows, row{
			text: m.textRow(column, "net", m.networkText()),
			strip: m.trendOf(func(t trend) float64 { return t.net }, rateScale,
				func(float64) lipgloss.Style { return theme.Cyan }),
		})
	}
	if metrics.HasPressure() {
		rows = append(rows, row{text: m.textRow(column, "pressure", m.pressureText())})
	}
	if hottest, ok := metrics.Hottest(); ok {
		// The meter is the hottest sensor's share of its own limit, which is
		// the only honest way to compare two of them: an NVMe at 71 degrees
		// is closer to trouble than a CPU at 80.
		share := hottest.Share()
		rows = append(rows, row{text: m.meterRow(column, "temp", share,
			theme.Usage(share), m.temperatureText(hottest))})
	}
	rows = append(rows, m.gpuRows(column)...)
	if metrics.Uptime > 0 {
		rows = append(rows, row{text: m.textRow(column, "uptime", formatUptime(metrics.Uptime))})
	}
	// Last, because it is the row that answers no question about what is
	// wrong: the list is ordered by urgency and the box truncates from the
	// bottom, so what the machine calls itself is what should go last.
	if m.os != "" {
		rows = append(rows, row{text: m.textRow(column, "system", theme.Dim.Render(m.os))})
	}
	if m.stale {
		rows = append(rows, row{}, row{text: theme.Dim.Render(
			"  the last sample failed — these readings are the ones before it")})
	}
	// Whatever is left under the readings goes to the process list, which is
	// the one thing here that can use any amount of room and is worth
	// nothing at all in two lines.
	if m.height > 0 {
		if block := m.processBlock(m.height - len(rows) - 1); len(block) > 0 {
			rows = append(rows, row{})
			rows = append(rows, block...)
		}
	}
	return m.assemble(rows)
}

// assemble lays the rows out, putting the trend strips in a column at the
// right edge when every one of them fits.
//
// All or none, deliberately: a strip that appears on one row and not the
// next reads as data about that row rather than as the width running out.
// The strips are also the part that can be inferred from the next sample,
// so they are what a narrow terminal gives up rather than the numbers.
func (m *Model) assemble(rows []row) string {
	widest, strips := 0, false
	for _, entry := range rows {
		widest = max(widest, lipgloss.Width(entry.text))
		strips = strips || entry.strip != ""
	}
	stripWidth := 0
	for _, entry := range rows {
		stripWidth = max(stripWidth, lipgloss.Width(entry.strip))
	}
	room := m.width > 0 && widest+2+stripWidth <= m.width
	lines := make([]string, len(rows))
	for index, entry := range rows {
		lines[index] = entry.text
		if !strips || !room || entry.strip == "" {
			continue
		}
		gap := m.width - lipgloss.Width(entry.text) - lipgloss.Width(entry.strip)
		lines[index] = entry.text + strings.Repeat(" ", gap) + entry.strip
	}
	return strings.Join(lines, "\n")
}

// trendOf draws one reading's history as a strip, prefixed with the stretch
// of host time it covers. Every strip covers the same window, so the label
// is padded onto all of them rather than printed once: the strips have to
// start at the same column to read as a column.
func (m *Model) trendOf(of func(trend) float64, scale func([]float64) (float64, float64),
	style func(float64) lipgloss.Style) string {
	samples, seconds := m.history.window(sparkWidth)
	if len(samples) == 0 {
		return ""
	}
	values := make([]float64, len(samples))
	for index, sample := range samples {
		values[index] = of(sample)
	}
	floor, ceiling := scale(values)
	return theme.Dim.Render(sparkSpan(seconds)+" ") + sparkline(values, floor, ceiling, style)
}

// filesystems is the mount list, falling back to the root reading alone on a
// host whose full `df` did not answer — which is what the guard around it
// exists to survive.
func (m *Model) filesystems() []host.Filesystem {
	if len(m.metrics.Filesystems) > 0 {
		return m.metrics.Filesystems
	}
	if m.metrics.DiskTotalKB == 0 {
		return nil
	}
	return []host.Filesystem{{
		Mount: "/", TotalKB: m.metrics.DiskTotalKB, UsedKB: m.metrics.DiskUsedKB}}
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

// rowTextReserve is what a row's gauge leaves for the label and the text
// beside it. The longest of those is the pressure row — three readings and
// the word that says what they mean — and it is the one with no gauge to
// shrink, so the gauges shrink on its behalf: at a hundred columns a
// twenty-six cell bar is the difference between that row fitting and being
// cut mid-word.
const rowTextReserve = 64

// gaugeWidth is the bar every row draws, or leaves blank where it has none,
// so all of them start their text under the same column.
func (m *Model) gaugeWidth(column int) int {
	if m.width <= 0 {
		return detailBar
	}
	return min(max(m.width-column-rowTextReserve, meterMinBar), detailBar)
}

// meterRow is one reading: its name, its gauge, and the numbers the gauge
// cannot carry. The bar shows the percentage, so no row prints one twice —
// except the CPU, whose reading has no absolute amount to print instead.
func (m *Model) meterRow(column int, label string, percent float64, style lipgloss.Style, text string) string {
	return fmt.Sprintf(" %s [%s]  %s", pad(label, column),
		styledBar(percent, m.gaugeWidth(column), style), text)
}

// textRow is a reading with no meter, aligned so its text starts where the
// gauges do rather than under their labels.
func (m *Model) textRow(column int, label, text string) string {
	return fmt.Sprintf(" %s %s  %s", pad(label, column),
		strings.Repeat(" ", m.gaugeWidth(column)+2), text)
}

// coreRow is one cell per core, tallest for the busiest. It is the row that
// makes the average above it readable: one pinned core among eight idle ones
// is a machine with a problem and an average that says twelve percent.
//
// One cell per core rather than a labelled bar each, because a labelled bar
// each stops fitting somewhere around sixteen cores and the shape of the
// strip is what the row is read for. The number that matters is called out
// beside it.
func (m *Model) coreRow(column int) string {
	if len(m.usage.Cores) == 0 {
		return ""
	}
	var strip strings.Builder
	busiest, index := 0.0, 0
	for core, percent := range m.usage.Cores {
		strip.WriteString(theme.Usage(percent).Render(sparkCell(percent)))
		if percent > busiest {
			busiest, index = percent, core
		}
	}
	text := strip.String() + theme.Dim.Render(
		fmt.Sprintf("   busiest cpu%d at %.0f%%", index, busiest))
	return fmt.Sprintf(" %s %s", pad("cores", column), text)
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
		text += theme.Dim.Render(fmt.Sprintf("   busiest %s at %s", busiest, formatRate(rate)))
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
	text := fmt.Sprintf("%.0f°C %s", hottest.Celsius(), hottest.Name())
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
		rest = append(rest, fmt.Sprintf("%.0f°C %s", sensor.Celsius(), sensor.Name()))
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

// sparkCells are the eight heights a single cell can draw, which is what a
// per-core strip and (from C3) a history sparkline are both made of.
var sparkCells = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// sparkCell is one percentage as one cell.
func sparkCell(percent float64) string {
	index := int(percent / 100 * float64(len(sparkCells)))
	return sparkCells[min(max(index, 0), len(sparkCells)-1)]
}

// pad left-aligns a label in a fixed column, truncating one too long rather
// than pushing the gauge beside it out of line. It measures cells, not
// bytes, the way panel.fitLine does — a mount point can carry anything a
// filesystem can be named.
func pad(label string, column int) string {
	label = lipgloss.NewStyle().MaxWidth(column).Render(label)
	if gap := column - lipgloss.Width(label); gap > 0 {
		return label + strings.Repeat(" ", gap)
	}
	return label
}

// loadStyle colors the load meter. Per-CPU load is the normalization that
// makes 1.0 mean "full" on any machine, so the thresholds are the same shape
// as the usage ones without being percentages of anything.
func loadStyle(perCPU float64) lipgloss.Style {
	switch {
	case perCPU >= 1:
		return theme.Red
	case perCPU >= 0.7:
		return theme.Yellow
	default:
		return theme.Green
	}
}

func formatKB(kb uint64) string {
	units := []string{"K", "M", "G", "T", "P"}
	value, unit := float64(kb), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%s", value, units[unit])
	}
	return fmt.Sprintf("%.1f%s", value, units[unit])
}

// formatRate prints a throughput. It steps by 1024 like every other amount
// on this screen: network tools conventionally count in decimal units, but
// one screen showing two definitions of "K" is worse than either.
func formatRate(bytesPerSecond float64) string {
	units := []string{"B", "K", "M", "G", "T"}
	value, unit := bytesPerSecond, 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f%s/s", value, units[unit])
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%s/s", value, units[unit])
	}
	return fmt.Sprintf("%.1f%s/s", value, units[unit])
}

func formatUptime(duration time.Duration) string {
	switch {
	case duration >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(duration.Hours())/24, int(duration.Hours())%24)
	case duration >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(duration.Hours()), int(duration.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	}
}

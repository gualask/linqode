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
	"github.com/charmbracelet/x/ansi"

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
			disks := map[string]uint64{}
			for _, filesystem := range filesystemsOf(metrics) {
				disks[filesystem.Mount] = filesystem.UsedKB
			}
			m.history.push(trend{
				uptime: metrics.UptimeSeconds,
				cpu:    usage.CPUPercent,
				memory: metrics.MemUsedPercent(),
				rx:     usage.RxRate,
				tx:     usage.TxRate,
				disks:  disks,
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
		// With both rankings on screen there is nothing to switch to.
		if m.bothRankings() {
			return nil
		}
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
	if m.open && key.String() == "s" && !m.bothRankings() {
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

// View is the system view: the machine's readings, its recent history drawn
// tall, and what it is running, from the top.
//
// It fills the screen in both directions. The readings take two columns
// where the terminal is wide enough for two; the history is a row of charts
// as tall as the readings leave room for; and the processes take the rest,
// both rankings side by side where the width allows. What gives way on a
// short terminal is room's to decide: the charts first, because the next
// sample reconstructs them, then the processes.
//
// It is about the machine and nothing else. What docker holds on disk used to
// be a row here, and is under the services table now, with the rest of what
// is about docker.
func (m *Model) View() string {
	if !m.loaded {
		return theme.Dim.Render("  (waiting for the first host sample…)")
	}
	var lines []string
	if m.unavailable != "" {
		lines = append(lines,
			"  "+theme.Yellow.Render("compose is unavailable on this host"),
			"  "+theme.Dim.Render(m.unavailable),
			"")
	}
	lines = append(lines, m.readingLines()...)
	if m.stale {
		lines = append(lines, "", theme.Dim.Render(
			"  the last sample failed — these readings are the ones before it"))
	}
	chartHeight, processRows := m.room(m.height - len(lines))
	if chartHeight > 0 {
		lines = append(lines, "")
		lines = append(lines, m.chartLines(chartHeight)...)
	}
	if processRows > 0 {
		lines = append(lines, "")
		lines = append(lines, m.processLines(processRows)...)
	}
	return strings.Join(lines, "\n")
}

// columnGap separates two columns of anything in this view.
const columnGap = 4

// grid is where a row's parts go: how wide the label column is, and how wide
// the column of readings the row sits in.
type grid struct {
	label, width int
}

// gauge is the bar every row draws, or leaves blank where it has none, so all
// of them start their text under the same column.
func (g grid) gauge() int {
	if g.width <= 0 {
		return detailBar
	}
	return min(max(g.width-g.label-rowTextReserve, meterMinBar), detailBar)
}

// besideEachOther joins two columns of lines, the left one cut or padded to
// width so the right one starts in the same place on every line.
func besideEachOther(left, right []string, width int) []string {
	lines := make([]string, max(len(left), len(right)))
	for index := range lines {
		var l, r string
		if index < len(left) {
			l = fit(left[index], width)
		}
		if index < len(right) {
			r = fit(right[index], width)
		}
		if r == "" {
			lines[index] = l
			continue
		}
		lines[index] = l + strings.Repeat(" ", width-lipgloss.Width(l)+columnGap) + r
	}
	return lines
}

// fit cuts a line that is wider than its column, rather than letting it run
// into the column beside it.
func fit(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "…")
}

// rowTextReserve is what a row's gauge leaves for the label and the text
// beside it. The longest of those is the pressure row — three readings and
// the word that says what they mean — and it is the one with no gauge to
// shrink, so the gauges shrink on its behalf: at a hundred columns a
// twenty-six cell bar is the difference between that row fitting and being
// cut mid-word.
const rowTextReserve = 64

// pad left-aligns a label in a fixed column, truncating one too long rather
// than pushing the gauge beside it out of line. It measures cells, not
// bytes, the way panel.fitLine does — a mount point can carry anything a
// filesystem can be named.
//
// A path is cut at the *front*, because that is where its uninformative half
// is: `/System/Volumes/Data` and `/System/Volumes/Preboot` are the same
// string for sixteen cells and differ only in what a right-hand truncation
// would throw away, and `/var/lib/docker` beside `/var/lib/postgresql` is
// the same story on the hosts this usually runs against.
func pad(label string, column int) string {
	label = trimPath(label, column)
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

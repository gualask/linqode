package tui

// Container resource usage, in the two modes the status view offers.
//
// Soft: one `docker stats --no-stream` sample every statsPollInterval,
// feeding the table's CPU and MEM columns. A sample costs ~2 s on the
// server regardless of container count — the daemon reads the cgroups
// twice, a second apart, to derive a CPU percentage — so it runs on its own
// slow interval instead of alongside the ~60 ms `compose ps`.
//
// Live: the streaming form, started on request, which emits a block per
// second into a panel below the table with a per-container sparkline. While
// it runs the soft poll stands down: the stream already delivers fresher
// numbers for the same columns.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
)

const (
	// statsPollInterval is how often the soft sample runs. At 20 s a ~2 s
	// command is in flight a tenth of the time; halving it would double
	// that without telling the operator anything new, since each reading is
	// still an average over docker's own sampling second. Watching
	// fluctuation is what the live mode is for.
	statsPollInterval = 20 * time.Second
	// statsDrainInterval is how often pending live samples are applied.
	// Docker emits a block per second, so this only has to be fast enough
	// to feel live.
	statsDrainInterval = 250 * time.Millisecond
	// maxStatsPerTick bounds one drain, like the log view's, so a burst
	// cannot starve input handling.
	maxStatsPerTick = 1_000
	// historyLen is how many live samples are kept per container: four
	// minutes at docker's one-per-second cadence, more than any sparkline
	// can show, so a widening terminal reveals history instead of blanks.
	historyLen = 240
	// liveMaxRows caps the live panel so it cannot crowd out the table on a
	// project with many services.
	liveMaxRows = 8
)

// statsPollMsg is the soft interval firing.
type statsPollMsg struct{}

// statsSampleMsg is the outcome of one soft sample.
type statsSampleMsg struct {
	stats []compose.ContainerStats
	err   error
}

// statsTickMsg drains the live stream.
type statsTickMsg struct{}

func statsPollTick() tea.Cmd {
	return tea.Tick(statsPollInterval, func(time.Time) tea.Msg { return statsPollMsg{} })
}

func statsTick() tea.Cmd {
	return tea.Tick(statsDrainInterval, func(time.Time) tea.Msg { return statsTickMsg{} })
}

// statsSampleCmd takes one soft sample, or nil when none is configured.
func (m *statusModel) statsSampleCmd() tea.Cmd {
	fetch := m.statsFetch
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		stats, err := fetch()
		return statsSampleMsg{stats: stats, err: err}
	}
}

// refreshStats starts a soft sample unless one is already in flight or the
// live stream is running — the stream feeds the same columns, faster.
func (m *statusModel) refreshStats() tea.Cmd {
	if m.statsFetch == nil || m.statsRefreshing || m.liveActive() {
		return nil
	}
	m.statsRefreshing = true
	return m.statsSampleCmd()
}

// applySample replaces the readings with a whole sample. Containers absent
// from it are dropped rather than kept: a service that stopped between two
// samples must not keep showing the CPU it used while running.
func (m *statusModel) applySample(sample []compose.ContainerStats) {
	stats := make(map[string]compose.ContainerStats, len(sample))
	for _, s := range sample {
		stats[s.Name] = s
	}
	m.stats = stats
	m.statsLoaded = true
}

// liveActive reports whether the streaming mode is on, including the window
// between asking for it and the stream arriving.
func (m *statusModel) liveActive() bool {
	return m.statsFeed != nil || m.statsStarting
}

// statsColumns reports whether the table carries CPU and MEM. They are part
// of the table whenever anything can fill them, and show "-" until the
// first sample lands.
func (m *statusModel) statsColumns() bool {
	return m.statsFetch != nil || m.liveActive() || len(m.stats) > 0
}

// toggleLive turns the streaming panel on or off. Opening pays docker's ~2 s
// of sampling latency once and then streams; closing terminates the remote
// command, so nothing is sampled on the server while the panel is hidden.
func (m *statusModel) toggleLive() tea.Cmd {
	if m.liveActive() {
		m.stopLive()
		// The soft poll owns the columns again. The numbers just off the
		// stream are a second old, so the next scheduled sample is soon
		// enough — no need to spend 2 s of server time right now.
		return nil
	}
	m.statsStarting = true
	m.statsErr = ""
	command := compose.StatsCommand(m.info.ComposeDir)
	return func() tea.Msg { return openStatsMsg{command: command} }
}

// stopLive tears the stream down and forgets its history. The readings
// themselves survive only if the soft poll is there to refresh them;
// otherwise they would freeze on screen, and stale numbers read as live.
func (m *statusModel) stopLive() {
	if m.statsFeed != nil {
		m.statsFeed.Stop()
		m.statsFeed = nil
	}
	m.statsStarting = false
	m.history = nil
	m.statsErr = ""
	if m.statsFetch == nil {
		m.stats = nil
		m.statsLoaded = false
	}
}

// drainStats applies pending live samples, reporting whether the stream
// ended.
func (m *statusModel) drainStats() bool {
	for range maxStatsPerTick {
		select {
		case event, ok := <-m.statsFeed.Events:
			if !ok {
				return true
			}
			switch event.Kind {
			case LogLine:
				if stats, ok := compose.ParseStats(event.Text); ok {
					m.stats[stats.Name] = stats
					m.statsLoaded = true
					m.record(stats)
				}
			case LogStderrLine:
				// docker writes its complaints here; the last one is the
				// useful one.
				m.statsErr = event.Text
			case LogEnded:
				return true
			}
		default:
			return false
		}
	}
	return false
}

// record appends a container's CPU reading to its history, dropping the
// oldest once the window is full.
func (m *statusModel) record(stats compose.ContainerStats) {
	percent, ok := stats.CPUPercent()
	if !ok {
		return
	}
	if m.history == nil {
		m.history = map[string][]float64{}
	}
	series := append(m.history[stats.Name], percent)
	if len(series) > historyLen {
		series = series[len(series)-historyLen:]
	}
	m.history[stats.Name] = series
}

// statsCells are a container's CPU and memory readings, dimmed placeholders
// until its first sample arrives — a container can appear in `ps` before
// stats have been taken for it.
func (m *statusModel) statsCells(container string) []cell {
	stats, ok := m.stats[container]
	if !ok {
		return []cell{{text: "-", style: dimStyle}, {text: "-", style: dimStyle}}
	}
	cpu := cell{text: stats.CPUPerc}
	if percent, ok := stats.CPUPercent(); ok {
		cpu.style = usageStyle(percent)
	}
	mem := cell{text: stats.MemAmount()}
	if percent, ok := stats.MemPercent(); ok {
		mem.style = usageStyle(percent)
	}
	return []cell{cpu, mem}
}

// liveRows are the services the live panel has something to say about, in
// table order.
func (m *statusModel) liveRows() []compose.Service {
	var rows []compose.Service
	for _, s := range m.services {
		if _, ok := m.stats[s.Name]; ok {
			rows = append(rows, s)
			if len(rows) == liveMaxRows {
				break
			}
		}
	}
	return rows
}

// liveHeight is how many lines the panel needs: its rule, its rows, and a
// blank line separating it from the table.
func (m *statusModel) liveHeight() int {
	if !m.liveActive() {
		return 0
	}
	return 2 + max(len(m.liveRows()), 1)
}

// renderLivePanel is the streaming view below the table: current CPU and
// memory per container, with a sparkline of where the CPU has been.
func (m *statusModel) renderLivePanel(width int) string {
	rows := m.liveRows()

	var samples int
	for _, series := range m.history {
		samples = max(samples, len(series))
	}
	title := " live "
	if m.statsStarting {
		title = " live · starting… "
	} else if samples > 0 {
		title = fmt.Sprintf(" live · 1s · %d samples ", samples)
	}
	rule := dimStyle.Render(" ──" + title + strings.Repeat("─", max(width-len(title)-4, 0)))

	if len(rows) == 0 {
		hint := "(waiting for the first sample…)"
		if !m.statsStarting {
			hint = "(no running containers to sample)"
		}
		return rule + "\n" + dimStyle.Render("  "+hint)
	}

	nameWidth := 0
	for _, s := range rows {
		nameWidth = max(nameWidth, len(s.Service))
	}
	// Fixed columns: a leading space, then name, CPU, sparkline, memory and
	// peak separated by two spaces each. Whatever is left over goes to the
	// sparkline, which is dropped entirely when that is not enough to be
	// readable.
	const cpuWidth, memWidth, peakWidth = 7, 9, 11
	// The trailing column keeps the row off the system panel's border.
	fixed := 1 + nameWidth + 2 + cpuWidth + 2 + memWidth + 2 + peakWidth + 1
	sparkWidth := width - fixed - 2 // the separator the sparkline brings with it
	if sparkWidth < 8 {
		sparkWidth = 0
	}

	lines := make([]string, 0, len(rows))
	for _, s := range rows {
		stats := m.stats[s.Name]
		series := m.history[s.Name]

		cpuText := fmt.Sprintf("%*s", cpuWidth, stats.CPUPerc)
		if percent, ok := stats.CPUPercent(); ok {
			cpuText = usageStyle(percent).Render(cpuText)
		}
		line := fmt.Sprintf(" %-*s  %s", nameWidth, s.Service, cpuText)
		if sparkWidth > 0 {
			line += "  " + cyanStyle.Render(sparkline(series, sparkWidth))
		}
		line += fmt.Sprintf("  %*s", memWidth, stats.MemAmount())
		if peak := peakOf(series); peak > 0 {
			line += dimStyle.Render(fmt.Sprintf("  peak %5.1f%%", peak))
		}
		lines = append(lines, line)
	}
	return rule + "\n" + strings.Join(lines, "\n")
}

// sparkRunes are the eight block heights, lightest first.
var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// flatBand is how much a series may vary, as a fraction of its peak, and
// still count as not varying at all.
const flatBand = 0.05

// sparkline renders the newest width values, one column each, scaled to the
// window's own peak rather than to 0–100%: fluctuation is the point, and a
// service oscillating between 0.1% and 0.4% is a flat line on an absolute
// axis. peakOf puts the scale back on screen.
//
// A series that barely moves draws along the baseline instead of filling
// the row: relative scaling would otherwise turn an idle container's noise
// into a wall of full blocks, which reads as saturation. The current value
// beside the line is what says how high "flat" is.
//
// Shorter histories are padded on the left, so the newest sample sits at the
// right edge and the line grows leftwards as samples accumulate instead of
// sliding sideways.
func sparkline(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	peak := peakOf(values)
	flat := peak <= 0 || peak-lowOf(values) <= peak*flatBand

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	for _, v := range values {
		level := 0
		if !flat {
			level = int(v/peak*float64(len(sparkRunes)-1) + 0.5)
			level = min(max(level, 0), len(sparkRunes)-1)
		}
		b.WriteRune(sparkRunes[level])
	}
	return b.String()
}

// lowOf is the smallest value in a series, 0 for an empty one.
func lowOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	low := values[0]
	for _, v := range values {
		low = min(low, v)
	}
	return low
}

// peakOf is the highest value in a series, 0 for an empty one.
func peakOf(values []float64) float64 {
	peak := 0.0
	for _, v := range values {
		peak = max(peak, v)
	}
	return peak
}

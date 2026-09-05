package system

// What the machine is running, in the view that is opened to ask.
//
// This is the first reading in the plan that is not on the home at all. It
// costs about 220 bytes per process — 20 KB on an ordinary host — which is
// far too much to pay every five seconds for a screen nobody is looking at,
// and perfectly reasonable to pay while an operator is looking at exactly
// this. The sampler's gate is what makes the difference: the source is not
// due while the view is closed, and is read the moment it opens.
//
// On a Docker host most of the heaviest processes *are* the containers
// already in the table on the home. That is not a reason to leave them out:
// the table says which container is using memory, and this says which process
// inside it is — and it is also the only thing on the screen that can account
// for what the containers do not, which is the usual answer when the numbers
// on the home do not add up.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/theme"
)

// processRows is the most the list will ever draw. Past this it stops being
// a summary and starts being a worse version of the table above it; what an
// operator wants from this block is the handful at the top.
const processRows = 12

// The columns of the process list.
const (
	processNameWidth = 20
	processNumWidth  = 9
)

// ranking is what the list is sorted by. Both orders answer a real question
// and neither answers the other's: memory is who is holding the machine's
// RAM, CPU is who is burning it right now.
type ranking int

const (
	byMemory ranking = iota
	byCPU
)

func (r ranking) String() string {
	if r == byCPU {
		return "by cpu"
	}
	return "by memory"
}

// SetProcesses applies one reading of the process table. Like every other
// counter here, the CPU share comes from the difference against the previous
// one; a failed read leaves what is on screen alone rather than blanking it.
func (m *Model) SetProcesses(sample host.ProcessSample, err error) {
	if err != nil {
		m.processesStale = true
		return
	}
	m.processes = sample.UsageSince(m.previousProcesses)
	m.previousProcesses = sample
	m.processesStale = false
}

// SetOpen tells the panel whether it is the view on screen or the band in the
// header. The two answer to different keys, and the footer has to say which.
func (m *Model) SetOpen(open bool) { m.open = open }

// toggleRanking switches which question the list is answering.
func (m *Model) toggleRanking() {
	if m.ranking == byMemory {
		m.ranking = byCPU
		return
	}
	m.ranking = byMemory
}

// ranked is the processes worth showing, in the order asked for. Sorting is
// done here rather than on the server because the field the server would sort
// on is not reliably where it counts: see host.ProcessCommand.
func (m *Model) ranked() []host.ProcessUsage {
	entries := slices.Clone(m.processes)
	slices.SortFunc(entries, func(a, b host.ProcessUsage) int {
		if m.ranking == byCPU && a.CPUPercent != b.CPUPercent {
			// Descending, so the answer is the first line rather than the
			// last.
			if a.CPUPercent > b.CPUPercent {
				return -1
			}
			return 1
		}
		if a.RSSKB != b.RSSKB {
			if a.RSSKB > b.RSSKB {
				return -1
			}
			return 1
		}
		// A stable tail, so a list of idle processes does not reshuffle
		// itself every time it is drawn.
		return a.PID - b.PID
	})
	return entries
}

// blockHeadings is what the list costs before its first process: the name of
// the block and the row naming its columns.
const blockHeadings = 2

// processBlock is the heading and the rows, given the space left under the
// readings. It returns nothing when there is no room for the headings and a
// couple of processes: half a list is worse than none, because the thing it
// is read for is the top of it.
func (m *Model) processBlock(rows int) []row {
	if len(m.processes) == 0 || rows < blockHeadings+2 {
		return nil
	}
	entries := m.ranked()
	shown := min(min(rows-blockHeadings, processRows), len(entries))

	heading := fmt.Sprintf("  %s %s %s   %s",
		pad("process", processNameWidth),
		right("RSS", processNumWidth), right("CPU", processNumWidth),
		theme.Dim.Render("pid"))
	block := []row{{text: " " + theme.Bold.Render("processes") +
		theme.Dim.Render("  "+m.ranking.String()+
			fmt.Sprintf("   %d running", len(m.processes)))},
		{text: theme.Dim.Render(heading)}}

	for _, entry := range entries[:shown] {
		block = append(block, row{text: m.processLine(entry)})
	}
	if m.processesStale {
		block = append(block, row{text: theme.Dim.Render(
			"  the last process reading failed — this list is the one before it")})
	}
	return block
}

func (m *Model) processLine(entry host.ProcessUsage) string {
	// A share is measured against the machine's cores the way top measures
	// it, so a process on two full cores reads 200% and the number does not
	// change meaning with the size of the host.
	share := theme.Dim.Render(right("–", processNumWidth))
	if entry.Measured {
		share = cpuShareStyle(entry.CPUPercent).Render(
			right(fmt.Sprintf("%.1f%%", entry.CPUPercent), processNumWidth))
	}
	return fmt.Sprintf("  %s %s %s   %s",
		pad(entry.Name, processNameWidth),
		memoryStyle(entry.RSSKB, m.metrics.MemTotalKB).Render(
			right(formatKB(entry.RSSKB), processNumWidth)),
		share,
		theme.Dim.Render(fmt.Sprintf("%d", entry.PID)))
}

// cpuShareStyle colors a process's share of a core. The thresholds are not
// theme.Usage's: a process at 90% of one core is ordinary on an eight-core
// machine, where 90% of the *machine* is not.
func cpuShareStyle(percent float64) lipgloss.Style {
	switch {
	case percent >= 90:
		return theme.Red
	case percent >= 40:
		return theme.Yellow
	default:
		return theme.Green
	}
}

// memoryStyle colors a process's memory by what share of the machine it is
// holding — which is the thing worth noticing, and is why it needs the
// machine's total rather than a fixed number of gigabytes.
func memoryStyle(rssKB, totalKB uint64) lipgloss.Style {
	if totalKB == 0 {
		return lipgloss.NewStyle()
	}
	return theme.Usage(float64(rssKB) / float64(totalKB) * 100 * memoryShareScale)
}

// memoryShareScale is what makes that colouring say something. A single
// process holding a quarter of a machine is remarkable long before a
// filesystem at a quarter full is, so the same thresholds are reached four
// times sooner.
const memoryShareScale = 4

// right pads a value into a right-aligned column.
func right(text string, width int) string {
	text = lipgloss.NewStyle().MaxWidth(width).Render(text)
	if gap := width - lipgloss.Width(text); gap > 0 {
		return strings.Repeat(" ", gap) + text
	}
	return text
}

package status

// What docker holds on disk, under the table.
//
// "Is docker filling my disk?" is one of the most common questions about a
// deployment that has stopped, and the machine's filesystem rows cannot
// answer it: they say /var is at 93%, not that sixty gigabytes of it are
// images nobody runs. Only the daemon knows that. It used to be a row among
// the machine's readings, which mixed a question about docker into a view
// about the host; it belongs with the project, where the other docker
// readings are.
//
// It takes only rows the table leaves empty. A project long enough to fill
// the panel keeps every row of its table, and the section — and the reading
// behind it — waits for a terminal with room.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/tui/spark"
	"github.com/gualask/linqode/internal/tui/theme"
)

// diskKinds are the daemon's rows, in the order they are worth reading:
// images fill a disk first and by the most, and build cache is the one people
// forget exists.
var diskKinds = []struct{ kind, label string }{
	{"Images", "images"},
	{"Containers", "containers"},
	{"Local Volumes", "volumes"},
	{"Build Cache", "build cache"},
}

const (
	diskLabelWidth = 11 // "build cache"
	diskSizeWidth  = 9
	// diskMeter is the width of a kind's reclaimable gauge. Twenty cells
	// read a share at a glance; more is resolution nobody acts on.
	diskMeter = 20
)

// SetDiskUsage applies the daemon's own accounting of what it is holding. A
// failed read leaves the last answer alone: it changes slowly, so one that is
// a minute old is still worth reading.
func (m *Model) SetDiskUsage(usage []compose.DiskUsage, err error) {
	if err != nil || len(usage) == 0 {
		return
	}
	m.diskUsage = usage
}

// DiskUsageRoom reports whether the section would be drawn: compose is here,
// there is a table, and the panel has the section's rows to spare once the
// whole table and the live panel have theirs. An unknown size counts as room,
// the way every view here behaves until it is told how much it has.
func (m *Model) DiskUsageRoom() bool {
	if m.unavailable != "" || len(m.services) == 0 {
		return false
	}
	return m.height <= 0 || m.height-m.liveShare()-m.diskHeight() >= 1+len(m.services)
}

// diskHeight is what the section costs: a blank row, its rule, and a row per
// kind. Before the first answer it assumes every kind, so the room it is read
// for is the room it will need.
func (m *Model) diskHeight() int {
	kinds := 0
	for _, wanted := range diskKinds {
		if _, found := compose.Find(m.diskUsage, wanted.kind); found {
			kinds++
		}
	}
	if kinds == 0 {
		kinds = len(diskKinds)
	}
	return 2 + kinds
}

// renderDisk is the section: totals on its rule, then a row per kind — its
// size, a meter for the share of it the daemon would give back, the amount,
// and the counts behind it.
//
// The meter is the reclaimable share because that is the question the
// section exists to answer, and a share of something finite is the one thing
// a meter can honestly draw. The sizes are the daemon's own strings, the rule
// the container columns follow: one screen showing two roundings of the same
// number is worse than either.
func (m *Model) renderDisk(width int) string {
	title := " docker disk "
	if held, reclaimable, ok := compose.Totals(m.diskUsage); ok && held > 0 {
		title = fmt.Sprintf(" docker disk · %s of %s reclaimable ",
			compose.FormatBytes(reclaimable), compose.FormatBytes(held))
	}
	lines := []string{"", theme.Dim.Render(" ──" + title +
		strings.Repeat("─", max(width-lipgloss.Width(title)-4, 0)))}
	for _, wanted := range diskKinds {
		entry, found := compose.Find(m.diskUsage, wanted.kind)
		if !found {
			continue
		}
		line := fmt.Sprintf(" %-*s  %*s  ", diskLabelWidth, wanted.label, diskSizeWidth, entry.Size)
		size, free, sized := entry.Bytes()
		if sized && size > 0 {
			percent := float64(free) / float64(size) * 100
			line += "[" + spark.Meter(percent, diskMeter, theme.Usage(percent)) + "]"
		} else {
			line += strings.Repeat(" ", diskMeter+2)
		}
		if entry.HasReclaimable() {
			amount, _, _ := strings.Cut(entry.Reclaimable, " ")
			line += fmt.Sprintf("  %*s reclaimable", diskSizeWidth, amount)
		} else {
			line += theme.Dim.Render(fmt.Sprintf("  %*s", diskSizeWidth+len(" reclaimable"),
				"nothing to reclaim"))
		}
		if counts := diskCounts(entry); counts != "" {
			line += theme.Dim.Render("   " + counts)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// diskCounts is how many of a kind there are and how many are idle. The idle
// count belongs to the kinds with something to free: a volume store entirely
// in use must not read as one waiting to be cleaned.
func diskCounts(entry compose.DiskUsage) string {
	switch {
	case entry.Total == 0:
		return ""
	case entry.Idle() > 0 && entry.HasReclaimable():
		return fmt.Sprintf("%d idle of %d", entry.Idle(), entry.Total)
	default:
		return fmt.Sprintf("all %d in use", entry.Total)
	}
}

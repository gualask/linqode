package status

// Tests for how the table lays itself out: full width, right-aligned numbers,
// a heading band across its whole width, and a column gap that adapts.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The table owns the full width now that nothing sits beside it.
func TestTableKeepsItsColumnsAtFullWidth(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetSize(130, 24)
	m.SetServices(services("web", "db"), nil)
	m.SetStats(sample(reading("web", "12.34%", "153.6MiB")), nil)

	view := m.View()
	for _, want := range []string{"SERVICE", "STATE", "HEALTH", "CPU", "MEM", "NET RX/TX", "IO R/W", "STATUS"} {
		if !strings.Contains(view, want) {
			t.Errorf("column %q missing:\n%s", want, view)
		}
	}
}

// Numbers are compared by scanning down a column, which only works when
// their digits line up. The live panel already prints its readings this
// way; the table used to disagree with the panel beside it.
func TestNumericColumnsAlignRight(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetSize(140, 24)
	m.SetServices(services("web", "db"), nil)
	m.SetStats(sample(
		reading("web", "112.34%", "1.234GiB"),
		reading("db", "3.02%", "64MiB"),
	), nil)

	// The short readings are pushed right so their last characters sit
	// under the long ones', rather than every cell starting at its column's
	// left edge.
	view := m.View()
	long := strings.Index(view, "112.34%") + len("112.34%")
	short := strings.Index(view, "3.02%") + len("3.02%")
	lineStart := func(at int) int { return strings.LastIndex(view[:at], "\n") + 1 }
	if long-lineStart(long) != short-lineStart(short) {
		t.Errorf("CPU readings do not end in the same column:\n%s", view)
	}
	longMem := strings.Index(view, "1.234GiB") + len("1.234GiB")
	shortMem := strings.Index(view, "64MiB") + len("64MiB")
	if longMem-lineStart(longMem) != shortMem-lineStart(shortMem) {
		t.Errorf("memory readings do not end in the same column:\n%s", view)
	}
}

// The table's heading is a band across the whole terminal: a table narrower
// than the screen must not look like it was cut off part way.
func TestTableHeadingSpansTheWidth(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetSize(160, 24)
	m.SetServices(services("web"), nil)

	var heading string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "SERVICE") {
			heading = line
			break
		}
	}
	if heading == "" {
		t.Fatalf("no heading row in:\n%s", m.View())
	}
	if got := lipgloss.Width(heading); got != 160 {
		t.Errorf("heading spans %d of 160 columns: %q", got, heading)
	}
}

// The gap widens when the terminal can afford it and narrows when the
// alternative is ellipsizing content. Breathing room is worth having, but
// not at the price of the readings it separates.
func TestColumnGapAdaptsToWidth(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetServices(services("web", "db"), nil)
	m.SetStats(sample(reading("web", "12.34%", "153.6MiB")), nil)

	headers := m.tableHeaders()
	rows := [][]cell{m.serviceRow(m.services[0]), m.serviceRow(m.services[1])}

	_, wide := columnLayout(headers, rows, 200)
	if wide != columnGaps[0] {
		t.Errorf("gap at 200 columns is %d, want the widest %d", wide, columnGaps[0])
	}
	_, tight := columnLayout(headers, rows, 60)
	if tight != columnGaps[len(columnGaps)-1] {
		t.Errorf("gap at 60 columns is %d, want the narrowest %d",
			tight, columnGaps[len(columnGaps)-1])
	}

	// Whatever gap it picks, the row must still fit.
	for _, width := range []int{60, 80, 100, 120, 140, 200} {
		widths, gap := columnLayout(headers, rows, width)
		total := 1 + gap*(len(widths)-1)
		for _, columnWidth := range widths {
			total += columnWidth
		}
		if total != width {
			t.Errorf("at %d columns the row totals %d (gap %d)", width, total, gap)
		}
	}
}

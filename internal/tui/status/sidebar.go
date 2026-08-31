package status

// The system panel: the machine's own resources down the right-hand side of
// the status view, next to the service table.
//
// It shows the same sample as the compact header line (renderHostLine) and
// replaces it when the terminal is wide enough — below that the table needs
// every column it can get, so the line comes back instead.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gualask/linqode/internal/tui/theme"
)

const (
	// sidebarWidth is the whole right-hand column, border included.
	sidebarWidth = 30
	// sidebarInner is what to set Width() to: lipgloss counts padding in a
	// block's width but draws the border outside it, so the style has to be
	// one column narrower than the space the column occupies.
	sidebarInner = sidebarWidth - 1
	// sidebarContent is the room left for text: border, left padding, and a
	// column of breathing room before the screen edge.
	sidebarContent = sidebarWidth - 3
	// sidebarMinTable is how much table must survive for the panel to be
	// worth its width: enough for the service, state, health, CPU and MEM
	// columns plus a readable STATUS.
	sidebarMinTable = 70
)

// sidebarOn reports whether the system panel is shown. It needs a host
// sample to show and a terminal wide enough not to squeeze the table.
func (m *Model) sidebarOn() bool {
	return m.hostFetch != nil && m.width >= sidebarWidth+sidebarMinTable
}

// renderSystemPanel is the panel's text, unpadded and unbordered — the view
// places it.
func (m *Model) renderSystemPanel() string {
	var lines []string
	add := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	header := theme.Bold.Render("system")
	if m.metricsStale {
		header += theme.Dim.Render("  (stale)")
	}
	add("%s", header)
	add("")

	if !m.metricsLoaded {
		add("%s", theme.Dim.Render("(sampling…)"))
	} else {
		lines = append(lines, m.systemMetricLines()...)
	}

	if summary := m.projectSummary(); len(summary) > 0 {
		add("")
		lines = append(lines, summary...)
	}
	return strings.Join(lines, "\n")
}

// systemMetricLines is one labelled reading per resource, each followed by
// its bar. A reading the host did not report is dropped entirely, like in
// the compact line.
func (m *Model) systemMetricLines() []string {
	metrics := m.metrics
	var lines []string
	add := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	if metrics.HasLoad() {
		perCPU := metrics.LoadPerCPU()
		add("%-5s %s %s", "load",
			loadStyle(perCPU).Render(fmt.Sprintf("%.2f", metrics.Load1)),
			theme.Dim.Render(fmt.Sprintf("%.2f/cpu", perCPU)))
		// Load is not a percentage of anything, but per-CPU load is: 1.0 is
		// a full machine, and beyond that the bar simply stays full.
		add("%s", loadStyle(perCPU).Render(bar(perCPU*100, sidebarContent)))
		add("")
	}
	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		add("%-5s %s/%s %s", "mem",
			formatKB(metrics.MemUsedKB()), formatKB(metrics.MemTotalKB),
			usageStyle(percent).Render(fmt.Sprintf("%.0f%%", percent)))
		add("%s", usageStyle(percent).Render(bar(percent, sidebarContent)))
		add("")
	}
	if metrics.DiskTotalKB > 0 {
		percent := metrics.DiskUsedPercent()
		add("%-5s %s/%s %s", "disk",
			formatKB(metrics.DiskUsedKB), formatKB(metrics.DiskTotalKB),
			usageStyle(percent).Render(fmt.Sprintf("%.0f%%", percent)))
		add("%s", usageStyle(percent).Render(bar(percent, sidebarContent)))
		add("")
	}
	if metrics.Uptime > 0 {
		add("%-5s %s", "up", theme.Dim.Render(formatUptime(metrics.Uptime)))
	}
	return lines
}

// projectSummary counts the services by state, with anything unhealthy
// called out: on a long table that one line is what says whether the
// project is in trouble.
func (m *Model) projectSummary() []string {
	if len(m.services) == 0 {
		return nil
	}
	states := map[string]int{}
	unhealthy := 0
	for _, s := range m.services {
		states[s.State]++
		if s.Health == "unhealthy" {
			unhealthy++
		}
	}
	names := make([]string, 0, len(states))
	for state := range states {
		names = append(names, state)
	}
	// Lifecycle order, not alphabetical: "4 running · 1 exited" is how an
	// operator reads a project, and states docker may add later still get a
	// stable place at the end.
	sort.SliceStable(names, func(i, j int) bool {
		ri, rj := stateRank(names[i]), stateRank(names[j])
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})

	var parts []string
	for _, state := range names {
		parts = append(parts, stateStyle(state).Render(fmt.Sprintf("%d %s", states[state], state)))
	}
	lines := []string{strings.Join(parts, theme.Dim.Render(" · "))}
	if unhealthy > 0 {
		lines = append(lines, theme.Red.Render(fmt.Sprintf("%d unhealthy", unhealthy)))
	}
	return lines
}

// stateRank orders the states the summary can hold; anything unknown sorts
// after them.
func stateRank(state string) int {
	for i, known := range []string{"running", "restarting", "paused", "created", "exited", "dead"} {
		if state == known {
			return i
		}
	}
	return 100
}

// bar renders a percentage as a filled block gauge, clamped at both ends so
// an over-100% reading (load above one per core) stays a full bar rather
// than overflowing the column.
func bar(percent float64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := int(percent/100*float64(width) + 0.5)
	filled = min(max(filled, 0), width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

package tui

// Tests for the system panel: when it takes over from the compact host
// line, what it reports, and that it never pushes the table off screen.

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
)

// headerLine is the second line of the view, where the compact host line
// lives when there is no panel to hold it.
func headerLine(view string) string {
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		return ""
	}
	return lines[1]
}

func TestSidebarReplacesHostLineWhenWide(t *testing.T) {
	m := withHostFetch()
	m.setSize(120, 24)
	m.update(servicesMsg{services: services("web")})
	m.update(hostMsg{metrics: sampleMetrics()})

	view := m.view()
	if !strings.Contains(view, "system") {
		t.Errorf("system panel missing on a wide terminal:\n%s", view)
	}
	for _, want := range []string{"load", "mem", "disk", "up", "1h30m", "█"} {
		if !strings.Contains(view, want) {
			t.Errorf("panel missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(headerLine(view), "load") {
		t.Errorf("compact host line kept alongside the panel:\n%s", view)
	}
}

// Below the threshold the table needs every column it can get, so the
// compact line comes back and the panel goes away.
func TestSidebarYieldsToTheTableWhenNarrow(t *testing.T) {
	m := withHostFetch()
	m.setSize(80, 24)
	m.update(servicesMsg{services: services("web")})
	m.update(hostMsg{metrics: sampleMetrics()})

	view := m.view()
	if strings.Contains(view, "system") {
		t.Errorf("panel shown on a narrow terminal:\n%s", view)
	}
	if !strings.Contains(headerLine(view), "load 0.50") {
		t.Errorf("compact host line missing on a narrow terminal:\n%s", view)
	}
}

// Host metrics off means no panel, however wide the terminal is: the whole
// point of that switch is that nothing extra runs on the server.
func TestNoSidebarWithoutHostMetrics(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m.setSize(200, 24)
	m.update(servicesMsg{services: services("web")})

	if strings.Contains(m.view(), "system") {
		t.Errorf("panel shown with host metrics disabled:\n%s", m.view())
	}
}

func TestSidebarBeforeFirstSample(t *testing.T) {
	m := withHostFetch()
	m.setSize(120, 24)
	m.update(servicesMsg{services: services("web")})

	if !strings.Contains(m.view(), "sampling") {
		t.Errorf("panel does not say it is waiting for its first sample:\n%s", m.view())
	}
}

func TestSidebarFlagsStaleSample(t *testing.T) {
	m := withHostFetch()
	m.setSize(120, 24)
	m.update(hostMsg{metrics: sampleMetrics()})
	m.update(hostMsg{err: errors.New("connection lost")})

	view := m.view()
	if !strings.Contains(view, "stale") {
		t.Errorf("stale sample not flagged in the panel:\n%s", view)
	}
	if !strings.Contains(view, "load") {
		t.Errorf("stale sample blanked the panel:\n%s", view)
	}
}

// The summary is what says whether the project is in trouble without
// reading every row.
func TestSidebarSummarisesTheProject(t *testing.T) {
	m := withHostFetch()
	m.setSize(120, 24)
	m.update(servicesMsg{services: []compose.Service{
		{Service: "web", Name: "app-web-1", State: "running", Status: "Up"},
		{Service: "api", Name: "app-api-1", State: "running", Status: "Up"},
		{Service: "cache", Name: "app-cache-1", State: "running", Health: "unhealthy", Status: "Up"},
		{Service: "migrate", Name: "app-migrate-1", State: "exited", Status: "Exited (0)"},
	}})

	view := m.view()
	for _, want := range []string{"3 running", "1 exited", "1 unhealthy"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary missing %q:\n%s", want, view)
		}
	}
}

// Whatever the panel holds, the view must still fit the terminal: a line
// wider than the screen wraps and shifts everything below it.
func TestSidebarKeepsTheViewWithinTheTerminal(t *testing.T) {
	m := withHostFetch()
	m.statsFetch = func() ([]compose.ContainerStats, error) { return nil, nil }
	m.setSize(110, 24)
	m.update(servicesMsg{services: services("web", "db", "cache")})
	m.update(hostMsg{metrics: sampleMetrics()})
	m.update(statsSampleMsg{stats: sample(reading("web", "12.34%", "153.6MiB"))})

	for i, line := range strings.Split(m.view(), "\n") {
		if width := lipgloss.Width(line); width > 110 {
			t.Errorf("line %d is %d columns wide, terminal is 110: %q", i, width, line)
		}
	}
	// The table keeps its columns next to the panel.
	view := m.view()
	for _, want := range []string{"SERVICE", "STATE", "HEALTH", "CPU", "MEM", "STATUS"} {
		if !strings.Contains(view, want) {
			t.Errorf("column %q lost to the panel:\n%s", want, view)
		}
	}
}

func TestBarClamps(t *testing.T) {
	if got := bar(0, 4); got != "░░░░" {
		t.Errorf("bar(0) = %q", got)
	}
	if got := bar(100, 4); got != "████" {
		t.Errorf("bar(100) = %q", got)
	}
	// Load can exceed one per core; the bar fills rather than overflowing.
	if got := bar(250, 4); got != "████" {
		t.Errorf("bar(250) = %q", got)
	}
	if got := bar(50, 4); got != "██░░" {
		t.Errorf("bar(50) = %q", got)
	}
	if got := bar(50, 0); got != "" {
		t.Errorf("bar with no width = %q", got)
	}
}

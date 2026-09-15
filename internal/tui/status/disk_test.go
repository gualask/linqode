package status

import (
	"errors"
	"strings"
	"testing"

	"github.com/gualask/linqode/internal/compose"
)

func holding() []compose.DiskUsage {
	return compose.ParseSystemDF([]byte(
		"Images|12|3|48.2GB|31.4GB (65%)\n" +
			"Containers|9|4|1.2GB|400MB (33%)\n" +
			"Local Volumes|4|4|8.9GB|0B\n" +
			"Build Cache|31|0|6.1GB|6.1GB (100%)\n"))
}

// What docker holds sits under the table, with the share it would give back
// on its rule and a row per kind.
func TestDockerDiskSitsUnderTheTable(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetSize(150, 20)
	m.SetServices(services("web", "db"), nil)
	m.SetDiskUsage(holding(), nil)

	view := m.View()
	section := strings.Index(view, "docker disk")
	if section < 0 {
		t.Fatalf("no docker disk section:\n%s", view)
	}
	if section < strings.Index(view, " db ") {
		t.Errorf("the section is above the table:\n%s", view)
	}
	for _, want := range []string{
		"37.9GB of 64.4GB reclaimable", // the decision a prune turns on, on the rule
		"images", "48.2GB", "31.4GB reclaimable", "9 idle of 12",
		"build cache", "31 idle of 31",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the section is missing %q:\n%s", want, view)
		}
	}
	// A volume store entirely in use must not be offered for cleanup.
	if !strings.Contains(view, "nothing to reclaim") || !strings.Contains(view, "all 4 in use") {
		t.Errorf("the fully used volume store is not said to be in use:\n%s", view)
	}
	if strings.Contains(view, "0 idle") {
		t.Errorf("an idle count of nothing was drawn:\n%s", view)
	}
}

// The section only takes rows the table leaves empty: a table that fills the
// panel keeps every row, and the section is neither drawn nor read for.
func TestDockerDiskNeverTakesATableRow(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetServices(services("web", "db", "cache", "worker"), nil)
	m.SetDiskUsage(holding(), nil)

	m.SetSize(150, 8)
	view := m.View()
	if strings.Contains(view, "docker disk") || m.DiskUsageRoom() {
		t.Errorf("the section was drawn in a panel the table needs:\n%s", view)
	}
	for _, service := range []string{"web", "db", "cache", "worker"} {
		if !strings.Contains(view, service) {
			t.Errorf("the table lost %q to the section:\n%s", service, view)
		}
	}

	m.SetSize(150, 11)
	if !m.DiskUsageRoom() || !strings.Contains(m.View(), "docker disk") {
		t.Errorf("a panel with exactly the room was refused it:\n%s", m.View())
	}
	if lines := strings.Count(m.View(), "\n") + 1; lines > 11 {
		t.Errorf("the panel drew %d lines in 11:\n%s", lines, m.View())
	}
}

// Nothing before the daemon answers, the last answer kept through a failure,
// and nothing at all on a host without compose.
func TestDockerDiskBeforeAndAfterAFailure(t *testing.T) {
	m := New(Config{Stats: true})
	m.SetSize(150, 20)
	m.SetServices(services("web"), nil)
	if strings.Contains(m.View(), "docker disk") {
		t.Errorf("a section was drawn before the daemon answered:\n%s", m.View())
	}
	m.SetDiskUsage(compose.ParseSystemDF([]byte("Images|1|1|2.0GB|0B\n")), nil)
	m.SetDiskUsage(nil, errors.New("daemon busy"))
	if !strings.Contains(m.View(), "2.0GB") {
		t.Errorf("a failed reading dropped the last good one:\n%s", m.View())
	}

	unavailable := New(Config{Unavailable: "docker is not installed on this host"})
	unavailable.SetSize(150, 20)
	if unavailable.DiskUsageRoom() {
		t.Error("a host without compose offered room for docker's disk")
	}
}

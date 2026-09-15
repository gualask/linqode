package home

import (
	"strings"
	"testing"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
)

// What docker holds on disk is read while the table has room to draw it, and
// not behind the system view, which is about the machine.
func TestDockerDiskIsReadWhereItIsDrawn(t *testing.T) {
	reads := 0
	config := Config{
		Host: func() (host.Metrics, error) { return sampleMetrics(), nil },
		DiskUsage: func() ([]compose.DiskUsage, error) {
			reads++
			return compose.ParseSystemDF([]byte("Images|2|1|1.0GB|500MB (50%)\n")), nil
		},
	}
	screen := screenWith(t, config, "api", "db")
	screen.SetSize(150, 30)
	sampleAll(screen)
	if reads == 0 {
		t.Fatal("the reading was never taken with the table on screen")
	}
	if !strings.Contains(screen.View(), "docker disk") {
		t.Errorf("the section is not under the table:\n%s", screen.View())
	}

	openSystemView(screen)
	if screen.diskUsageShown() {
		t.Error("the reading is still due behind the system view")
	}
	if strings.Contains(screen.View(), "docker disk") {
		t.Errorf("the system view shows docker's disk:\n%s", screen.View())
	}
}

// A project whose table fills the panel pays nothing for a section it has no
// room to draw.
func TestDockerDiskIsNotReadWithoutRoom(t *testing.T) {
	names := make([]string, 20)
	for index := range names {
		names[index] = "service" + string(rune('a'+index))
	}
	screen := screenWith(t, Config{DiskUsage: func() ([]compose.DiskUsage, error) {
		return nil, nil
	}}, names...)
	screen.SetSize(150, 24)
	screen.View()
	if screen.diskUsageShown() {
		t.Error("a table that fills the panel still leaves the reading due")
	}
}

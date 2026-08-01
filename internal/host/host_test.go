package host

import (
	"strings"
	"testing"
	"time"
)

// A full sample, as a real Linux host answers Command().
const fullOutput = `#load
0.15 0.09 0.08 1/234 5678
#uptime
3599.12 14000.50
#mem
MemTotal:        2048000 kB
MemAvailable:    1500000 kB
#cpu
2
#disk
Filesystem     1024-blocks    Used Available Capacity Mounted on
/dev/vda1         20509264 3145728  16316416      17% /
`

func TestParseFullSample(t *testing.T) {
	m, err := Parse([]byte(fullOutput))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if m.Load1 != 0.15 || m.Load5 != 0.09 || m.Load15 != 0.08 {
		t.Errorf("load = %v/%v/%v, want 0.15/0.09/0.08", m.Load1, m.Load5, m.Load15)
	}
	if m.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2", m.CPUs)
	}
	// Fractional seconds are dropped: the header shows whole units.
	if want := 3599 * time.Second; m.Uptime != want {
		t.Errorf("Uptime = %v, want %v", m.Uptime, want)
	}
	if m.MemTotalKB != 2048000 || m.MemAvailableKB != 1500000 {
		t.Errorf("mem = %d/%d, want 2048000/1500000", m.MemTotalKB, m.MemAvailableKB)
	}
	if m.DiskTotalKB != 20509264 || m.DiskUsedKB != 3145728 {
		t.Errorf("disk = %d/%d, want 20509264/3145728", m.DiskTotalKB, m.DiskUsedKB)
	}
}

func TestDerivedValues(t *testing.T) {
	m, err := Parse([]byte(fullOutput))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got := m.MemUsedKB(); got != 548000 {
		t.Errorf("MemUsedKB = %d, want 548000", got)
	}
	if got := m.MemUsedPercent(); got < 26.7 || got > 26.8 {
		t.Errorf("MemUsedPercent = %v, want ~26.76", got)
	}
	if got := m.DiskUsedPercent(); got < 15.3 || got > 15.4 {
		t.Errorf("DiskUsedPercent = %v, want ~15.34", got)
	}
	if got := m.LoadPerCPU(); got != 0.075 {
		t.Errorf("LoadPerCPU = %v, want 0.075", got)
	}
	if !m.HasLoad() {
		t.Error("HasLoad = false on a sample with a CPU count")
	}
}

// A host missing one source must still yield the rest: partial data beats
// no header at all.
func TestParseToleratesMissingSections(t *testing.T) {
	partial := `#load
0.50 0.40 0.30 1/100 200
#uptime
100.00
#mem
#cpu
4
#disk
`
	m, err := Parse([]byte(partial))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Load1 != 0.50 || m.CPUs != 4 {
		t.Errorf("usable sections lost: load=%v cpus=%d", m.Load1, m.CPUs)
	}
	if m.MemTotalKB != 0 || m.DiskTotalKB != 0 {
		t.Errorf("absent sections should stay zero, got mem=%d disk=%d",
			m.MemTotalKB, m.DiskTotalKB)
	}
	// Percentages must not divide by zero.
	if got := m.MemUsedPercent(); got != 0 {
		t.Errorf("MemUsedPercent on missing data = %v, want 0", got)
	}
	if got := m.DiskUsedPercent(); got != 0 {
		t.Errorf("DiskUsedPercent on missing data = %v, want 0", got)
	}
}

func TestParseToleratesGarbage(t *testing.T) {
	garbage := `#load
not a load average
#mem
MemTotal:
MemAvailable:    nonsense kB
#cpu
many
#disk
Filesystem     1024-blocks    Used Available Capacity Mounted on
overlay              wat     wat       wat      wat% /
`
	m, err := Parse([]byte(garbage))
	if err != nil {
		t.Fatalf("Parse should tolerate garbage, got %v", err)
	}
	if m != (Metrics{}) {
		t.Errorf("garbage produced non-zero metrics: %+v", m)
	}
}

func TestParseEmptyOutputFails(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Error("Parse(nil) should fail: an empty reply is not a sample")
	}
}

// MemAvailable above MemTotal is nonsense; it must not underflow into a
// huge unsigned value.
func TestMemUsedGuardsAgainstUnderflow(t *testing.T) {
	m := Metrics{MemTotalKB: 1000, MemAvailableKB: 2000}
	if got := m.MemUsedKB(); got != 0 {
		t.Errorf("MemUsedKB = %d, want 0 when available exceeds total", got)
	}
}

func TestCommandAsksForEverySection(t *testing.T) {
	command := Command()
	for _, marker := range []string{loadMarker, uptimeMarker, memMarker, cpuMarker, diskMarker} {
		if !strings.Contains(command, marker) {
			t.Errorf("Command() does not emit marker %q", marker)
		}
	}
	// -k pins df to 1024-byte blocks; without it the block size varies and
	// every disk number would be wrong by a factor of two.
	if !strings.Contains(command, "df -Pk /") {
		t.Error("Command() must use `df -Pk /` for portable, 1024-byte-block output")
	}
}

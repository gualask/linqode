package host

import "testing"

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
	if got := m.SwapUsedKB(); got != 124000 {
		t.Errorf("SwapUsedKB = %d, want 124000", got)
	}
	if got := m.SwapUsedPercent(); got < 12.1 || got > 12.2 {
		t.Errorf("SwapUsedPercent = %v, want ~12.11", got)
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

// A machine with no swap configured is not a machine with empty swap.
func TestNoSwapIsNotEmptySwap(t *testing.T) {
	m := Metrics{MemTotalKB: 1000}
	if m.SwapTotalKB != 0 || m.SwapUsedKB() != 0 || m.SwapUsedPercent() != 0 {
		t.Error("a host without swap should report nothing rather than zero of something")
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

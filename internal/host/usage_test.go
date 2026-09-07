package host

import (
	"strings"
	"testing"
)

// A percentage and a rate are the difference between two samples. Ten
// seconds of host time pass between these two.
func TestSinceDerivesPercentagesAndRates(t *testing.T) {
	before, _ := Parse([]byte(fullOutput))
	after, _ := Parse([]byte(strings.NewReplacer(
		"3599.12", "3609.12",
		"cpu  100 0 50 850", "cpu  450 0 50 2500",
		"cpu0 60 0 30 410", "cpu0 410 0 30 1060",
		"cpu1 40 0 20 440", "cpu1 40 0 20 1440",
		"  eth0: 1000000    2000", "  eth0: 1010000    2000",
	).Replace(fullOutput)))

	usage, ok := after.Since(before)
	if !ok {
		t.Fatal("two samples ten seconds apart measured nothing")
	}
	// Two cores, ten seconds, a hundred jiffies a second each: 2000
	// jiffies passed and 1650 of them were idle. One core at 35% is a
	// machine at 17.5%, which is the number a per-core reading exists to
	// go behind.
	if usage.CPUPercent != 17.5 {
		t.Errorf("CPUPercent = %v, want 17.5", usage.CPUPercent)
	}
	if len(usage.Cores) != 2 {
		t.Fatalf("cores = %v, want one entry each", usage.Cores)
	}
	// One core did all of it: exactly what a load average hides.
	if usage.Cores[0] != 35 || usage.Cores[1] != 0 {
		t.Errorf("cores = %v, want 35/0", usage.Cores)
	}
	// 10000 bytes over ten seconds, and only on the interface that carries
	// the machine's own traffic.
	if usage.RxRate != 1000 {
		t.Errorf("RxRate = %v, want 1000 B/s", usage.RxRate)
	}
	if usage.TxRate != 0 {
		t.Errorf("TxRate = %v, want nothing", usage.TxRate)
	}
}

// Loopback is not traffic, and a container's bytes cross a veth and a
// bridge before they reach the interface that already counted them.
func TestOnlyRealInterfacesAreSummed(t *testing.T) {
	for _, name := range []string{"lo", "veth9a1", "docker0", "br-1a2b3c", "tun0"} {
		if carriesHostTraffic(name) {
			t.Errorf("%s counted towards the machine's traffic", name)
		}
	}
	for _, name := range []string{"eth0", "ens3", "wlan0", "enp0s31f6"} {
		if !carriesHostTraffic(name) {
			t.Errorf("%s left out of the machine's traffic", name)
		}
	}
}

// The first sample after connecting has nothing to be measured against.
func TestSinceWithoutAPreviousSample(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	if _, ok := m.Since(Metrics{}); ok {
		t.Error("one sample yielded a rate")
	}
}

// A reboot restarts the uptime and every counter with it; the reading that
// straddles it must be dropped, not turned into an enormous rate.
func TestSinceAcrossAReboot(t *testing.T) {
	before, _ := Parse([]byte(fullOutput))
	after, _ := Parse([]byte(strings.Replace(fullOutput, "3599.12", "12.40", 1)))
	if _, ok := after.Since(before); ok {
		t.Error("a sample from before a reboot was measured against one from after")
	}
}

// A counter that went backwards without the clock doing so — an interface
// recreated under the same name — reports no movement rather than the
// difference read as unsigned.
func TestSinceIgnoresACounterThatWentBackwards(t *testing.T) {
	before, _ := Parse([]byte(fullOutput))
	after, _ := Parse([]byte(strings.NewReplacer(
		"3599.12", "3609.12",
		"  eth0: 1000000", "  eth0: 40000",
	).Replace(fullOutput)))
	usage, ok := after.Since(before)
	if !ok {
		t.Fatal("nothing measured")
	}
	if usage.RxRate != 0 {
		t.Errorf("RxRate = %v, want nothing from a reset counter", usage.RxRate)
	}
}

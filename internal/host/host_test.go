package host

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// A full sample, as a real Linux host answers Command(). The counters are
// round numbers so the deltas below can be checked by hand.
const fullOutput = `#load
0.15 0.09 0.08 1/234 5678
#uptime
3599.12 14000.50
#mem
MemTotal:        2048000 kB
MemAvailable:    1500000 kB
SwapTotal:       1024000 kB
SwapFree:         900000 kB
#cpu
2
#stat
cpu  100 0 50 850 0 0 0 0 0 0
cpu0 60 0 30 410 0 0 0 0 0 0
cpu1 40 0 20 440 0 0 0 0 0 0
intr 12345
#net
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  100000     100    0    0    0     0          0         0   100000     100    0    0    0     0       0          0
  eth0: 1000000    2000    0    0    0     0          0         0   500000    1500    0    0    0     0       0          0
veth9a1:   50000     100    0    0    0     0          0         0    25000      80    0    0    0     0       0          0
#pressure
/proc/pressure/cpu:some avg10=1.50 avg60=0.80 avg300=0.20 total=1234567
/proc/pressure/io:some avg10=0.30 avg60=0.10 avg300=0.05 total=98765
/proc/pressure/io:full avg10=0.20 avg60=0.05 avg300=0.01 total=54321
/proc/pressure/memory:some avg10=0.00 avg60=0.00 avg300=0.00 total=0
#disk
Filesystem     1024-blocks    Used Available Capacity Mounted on
/dev/vda1         20509264 3145728  16316416      17% /
#mounts
Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/vda1         20509264  3145728  16316416      17% /
devtmpfs           1000000        0   1000000       0% /dev
tmpfs              1024000    12000   1012000       2% /run
/dev/vdb1         98566144 88709529   9856615      90% /var
overlay           20509264  3145728  16316416      17% /var/lib/docker/overlay2/a1b2/merged
/dev/vda1         20509264  3145728  16316416      17% /mnt/bind
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
	// Fractional seconds are dropped from the duration the header shows, and
	// kept in the figure the rates are measured against.
	if want := 3599 * time.Second; m.Uptime != want {
		t.Errorf("Uptime = %v, want %v", m.Uptime, want)
	}
	if m.UptimeSeconds != 3599.12 {
		t.Errorf("UptimeSeconds = %v, want the undivided reading", m.UptimeSeconds)
	}
	if m.MemTotalKB != 2048000 || m.MemAvailableKB != 1500000 {
		t.Errorf("mem = %d/%d, want 2048000/1500000", m.MemTotalKB, m.MemAvailableKB)
	}
	if m.SwapTotalKB != 1024000 || m.SwapFreeKB != 900000 {
		t.Errorf("swap = %d/%d, want 1024000/900000", m.SwapTotalKB, m.SwapFreeKB)
	}
	if m.DiskTotalKB != 20509264 || m.DiskUsedKB != 3145728 {
		t.Errorf("disk = %d/%d, want 20509264/3145728", m.DiskTotalKB, m.DiskUsedKB)
	}
}

// The machine's line comes first, then one per core, and the jiffies that
// are counted twice in /proc/stat are counted once here.
func TestParseCPUTimes(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	if len(m.CPUTimes) != 3 {
		t.Fatalf("got %d cpu readings, want the machine and two cores: %+v", len(m.CPUTimes), m.CPUTimes)
	}
	machine := m.CPUTimes[0]
	if machine.Name != "cpu" {
		t.Errorf("first reading is %q, want the machine", machine.Name)
	}
	if machine.Total != 1000 || machine.Idle != 850 {
		t.Errorf("machine = %d/%d jiffies, want 1000/850", machine.Total, machine.Idle)
	}
	if m.CPUTimes[1].Name != "cpu0" || m.CPUTimes[2].Name != "cpu1" {
		t.Errorf("cores = %q/%q", m.CPUTimes[1].Name, m.CPUTimes[2].Name)
	}
	// `intr` also starts the line, and is not a CPU.
	for _, entry := range m.CPUTimes {
		if !strings.HasPrefix(entry.Name, "cpu") {
			t.Errorf("%q is not a cpu line", entry.Name)
		}
	}
}

// guest and guest_nice repeat time already counted in user and nice.
// Summing them would inflate the total and make every percentage too small.
func TestCPUTimesDoNotDoubleCountGuestTime(t *testing.T) {
	sample := "#stat\ncpu 100 0 50 850 0 0 0 0 90 10\n"
	m, _ := Parse([]byte(sample))
	if len(m.CPUTimes) != 1 || m.CPUTimes[0].Total != 1000 {
		t.Errorf("total = %+v, want 1000 with guest time left out", m.CPUTimes)
	}
}

func TestParseInterfaces(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	found := map[string]Interface{}
	for _, entry := range m.Interfaces {
		found[entry.Name] = entry
	}
	if len(found) != 3 {
		t.Fatalf("got %d interfaces, want lo, eth0 and the veth: %+v", len(found), m.Interfaces)
	}
	// The transmit column is the ninth number, not the second.
	if eth := found["eth0"]; eth.RxBytes != 1000000 || eth.TxBytes != 500000 {
		t.Errorf("eth0 = %d/%d, want 1000000/500000", eth.RxBytes, eth.TxBytes)
	}
}

func TestParsePressure(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	if !m.HasPressure() {
		t.Fatal("pressure was reported and not read")
	}
	if m.Pressure.CPU.Some10 != 1.50 {
		t.Errorf("cpu some = %v, want 1.50", m.Pressure.CPU.Some10)
	}
	if m.Pressure.IO.Some10 != 0.30 || m.Pressure.IO.Full10 != 0.20 {
		t.Errorf("io = %v/%v, want 0.30/0.20", m.Pressure.IO.Some10, m.Pressure.IO.Full10)
	}
	// The cpu file has no `full` line at all, and that is not a zero
	// reading of anything.
	if m.Pressure.CPU.Full10 != 0 {
		t.Errorf("cpu full = %v, want nothing", m.Pressure.CPU.Full10)
	}
}

// A kernel without PSI answers nothing, and nothing is what must be drawn.
func TestPressureAbsentOnAKernelWithoutIt(t *testing.T) {
	m, _ := Parse([]byte("#load\n0.1 0.1 0.1 1/1 1\n#pressure\n"))
	if m.HasPressure() {
		t.Error("HasPressure on a host that answered nothing")
	}
}

// The mount list is the two or three filesystems an operator watches, not
// the screenful of tmpfs and per-container overlays df prints.
func TestFilesystemsDropWhatIsNotStorage(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	var mounts []string
	for _, filesystem := range m.Filesystems {
		mounts = append(mounts, filesystem.Mount)
	}
	if want := []string{"/", "/var"}; !reflect.DeepEqual(mounts, want) {
		t.Errorf("filesystems = %v, want %v", mounts, want)
	}
}

// `/` on a containerised host is an overlay — the e2e fixture is one — so
// the rule that drops pseudo-filesystems must never reach the root.
func TestRootSurvivesEvenAsAnOverlay(t *testing.T) {
	sample := `#mounts
Filesystem     1024-blocks    Used Available Capacity Mounted on
overlay           20509264 3145728  16316416      17% /
tmpfs              1024000   12000   1012000       2% /dev/shm
`
	m, _ := Parse([]byte(sample))
	if len(m.Filesystems) != 1 || m.Filesystems[0].Mount != "/" {
		t.Fatalf("filesystems = %+v, want the overlay root alone", m.Filesystems)
	}
	// And it stands in for the root reading when its own df did not answer.
	if m.DiskTotalKB != 20509264 {
		t.Errorf("DiskTotalKB = %d, want the root from the mount list", m.DiskTotalKB)
	}
}

// Fullest is what the band's single disk meter shows: a comfortable root
// says nothing about the volume that is about to stop the deployment.
func TestFullestIsTheOneAboutToFill(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	fullest, ok := m.Fullest()
	if !ok {
		t.Fatal("no fullest filesystem on a sample with two")
	}
	if fullest.Mount != "/var" {
		t.Errorf("fullest = %q at %.0f%%, want /var", fullest.Mount, fullest.UsedPercent())
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
	if len(m.CPUTimes) != 0 || len(m.Interfaces) != 0 || len(m.Filesystems) != 0 {
		t.Errorf("absent sections produced readings: %+v", m)
	}
	// Percentages must not divide by zero.
	if got := m.MemUsedPercent(); got != 0 {
		t.Errorf("MemUsedPercent on missing data = %v, want 0", got)
	}
	if got := m.DiskUsedPercent(); got != 0 {
		t.Errorf("DiskUsedPercent on missing data = %v, want 0", got)
	}
	if _, ok := m.Fullest(); ok {
		t.Error("Fullest on a host that reported no filesystem")
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
#stat
cpu wat wat wat wat wat
#net
    lo: not a counter
#pressure
/proc/pressure/cpu:some avg10=plenty
#disk
Filesystem     1024-blocks    Used Available Capacity Mounted on
overlay              wat     wat       wat      wat% /
`
	m, err := Parse([]byte(garbage))
	if err != nil {
		t.Fatalf("Parse should tolerate garbage, got %v", err)
	}
	if !reflect.DeepEqual(m, Metrics{}) {
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
	for _, marker := range []string{loadMarker, uptimeMarker, memMarker, cpuMarker,
		statMarker, netMarker, pressureMarker, diskMarker, mountsMarker} {
		if !strings.Contains(command, marker) {
			t.Errorf("Command() does not emit marker %q", marker)
		}
	}
	// -k pins df to 1024-byte blocks; without it the block size varies and
	// every disk number would be wrong by a factor of two.
	if !strings.Contains(command, "df -Pk /") {
		t.Error("Command() must use `df -Pk /` for portable, 1024-byte-block output")
	}
	// A pressure file is absent on kernels without PSI, and its absence is
	// not an error worth putting on the operator's screen.
	if !strings.Contains(command, "2>/dev/null") {
		t.Error("a missing /proc/pressure would write to stderr")
	}
	// The mount list can block on a hung network mount. The root reading
	// must not be behind it, and a host without `timeout` must still get it.
	if !strings.Contains(command, "timeout 5 df -Pk") ||
		!strings.Contains(command, "else df -Pk") {
		t.Error("the mount list is neither guarded nor able to do without the guard")
	}
}

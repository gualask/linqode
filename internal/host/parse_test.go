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

package host

// Tests for the temperature readings.
//
// Unlike every other sample in this package, these are **not** captured from
// the e2e fixture. It has no hwmon and no thermal zones, and neither does the
// Linux VM under it — which is itself the common case this code has to
// survive, and which the e2e suite asserts. The samples below are written to
// the sysfs interface the kernel documents, in the shapes real chips produce:
// a coretemp with a package label and a critical point, an nvme whose limit
// is softer, a chip that measures something other than temperature, and a
// bare thermal zone with no limit at all.
//
// Real hardware is validated in the "hardening against real deployments"
// phase, which is the only place it honestly can be.

import (
	"strings"
	"testing"
)

const thermalOutput = `/sys/class/hwmon/hwmon0/name:acpitz
/sys/class/hwmon/hwmon0/temp1_input:41000
/sys/class/hwmon/hwmon1/name:coretemp
/sys/class/hwmon/hwmon1/temp1_input:58000
/sys/class/hwmon/hwmon1/temp1_label:Package id 0
/sys/class/hwmon/hwmon1/temp1_crit:100000
/sys/class/hwmon/hwmon1/temp1_max:84000
/sys/class/hwmon/hwmon2/name:nvme
/sys/class/hwmon/hwmon2/temp1_input:71000
/sys/class/hwmon/hwmon2/temp1_label:Composite
/sys/class/hwmon/hwmon2/temp1_max:84850
/sys/class/hwmon/hwmon3/name:nct6798
/sys/class/thermal/thermal_zone0/type:cpu_thermal
/sys/class/thermal/thermal_zone0/temp:47500
`

func thermalSample(t *testing.T) []Sensor {
	t.Helper()
	m, err := Parse([]byte("#thermal\n" + thermalOutput))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m.Sensors
}

func TestParseThermal(t *testing.T) {
	sensors := thermalSample(t)

	// Four directories carry a reading; the fifth is a motherboard chip
	// with a name and no temperature, which is a fan or a voltage.
	if len(sensors) != 4 {
		t.Fatalf("read %d sensors: %+v", len(sensors), sensors)
	}
	byName := map[string]Sensor{}
	for _, sensor := range sensors {
		byName[sensor.Name()] = sensor
	}

	cpu, ok := byName["Package id 0"]
	if !ok {
		t.Fatalf("the labelled sensor is missing: %+v", sensors)
	}
	if cpu.Chip != "coretemp" || cpu.Celsius() != 58 {
		t.Errorf("cpu = %+v", cpu)
	}
	// crit is the manufacturer's hard limit and max the softer one; the
	// hard one wins when the chip reports both.
	if cpu.LimitMilliC != 100_000 || !cpu.HasLimit() {
		t.Errorf("cpu limit = %d, want the critical point", cpu.LimitMilliC)
	}

	// A chip that reports only max still has a real limit.
	if drive := byName["Composite"]; drive.LimitMilliC != 84_850 {
		t.Errorf("nvme limit = %d, want its maximum", drive.LimitMilliC)
	}
	// A thermal zone has a type and no limit, and stands in on the ARM
	// boards that expose nothing else.
	zone := byName["cpu_thermal"]
	if zone.Celsius() != 47.5 {
		t.Errorf("thermal zone = %+v", zone)
	}
	if zone.HasLimit() {
		t.Error("a thermal zone reported a limit it does not have")
	}
	if zone.Limit() != defaultLimitMilliC {
		t.Errorf("a sensor with no limit is drawn against %d", zone.Limit())
	}
	// An unlabelled hwmon falls back to the chip's own name.
	if _, ok := byName["acpitz"]; !ok {
		t.Errorf("the unlabelled sensor lost its name: %+v", sensors)
	}
}

// A temperature is not a percentage of anything, and comparing two bare
// numbers gets the answer backwards: the NVMe at 71 is closer to trouble
// than the CPU at 58, and closer than the CPU would be at 80.
func TestTheHottestIsTheOneClosestToItsOwnLimit(t *testing.T) {
	m, _ := Parse([]byte("#thermal\n" + thermalOutput))
	hottest, ok := m.Hottest()
	if !ok {
		t.Fatal("no hottest sensor on a host reporting four")
	}
	if hottest.Name() != "Composite" {
		t.Errorf("hottest = %q at %.0f%% of its limit, want the nvme",
			hottest.Name(), hottest.Share())
	}
	if share := hottest.Share(); share < 83 || share > 85 {
		t.Errorf("share = %v, want ~84", share)
	}
	// And the list is in that order, so the view can take the front of it.
	for index := 1; index < len(m.Sensors); index++ {
		if m.Sensors[index-1].Share() < m.Sensors[index].Share() {
			t.Errorf("sensors are not ordered by share: %+v", m.Sensors)
			break
		}
	}
}

// Most virtual machines report nothing at all. No reading, no meter — and
// certainly no zero degrees.
func TestNoSensorsIsNotZeroDegrees(t *testing.T) {
	m, err := Parse([]byte("#load\n0.1 0.1 0.1 1/1 1\n#thermal\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Sensors) != 0 {
		t.Errorf("a host with no sensors produced %+v", m.Sensors)
	}
	if _, ok := m.Hottest(); ok {
		t.Error("Hottest on a host with no sensors")
	}
}

// A reading that is not a number is one sensor missing, not a sample lost.
func TestParseThermalSkipsWhatItCannotRead(t *testing.T) {
	m, _ := Parse([]byte(`#thermal
/sys/class/hwmon/hwmon0/name:coretemp
/sys/class/hwmon/hwmon0/temp1_input:warm
/sys/class/hwmon/hwmon1/name:nvme
/sys/class/hwmon/hwmon1/temp1_input:71000
/sys/class/hwmon/hwmon1/temp1_crit:not a number
`))
	if len(m.Sensors) != 1 || m.Sensors[0].Chip != "nvme" {
		t.Fatalf("got %+v, want only the readable one", m.Sensors)
	}
	if m.Sensors[0].HasLimit() {
		t.Error("an unreadable limit became a limit")
	}
}

func TestCommandAsksForTemperatures(t *testing.T) {
	command := Command()
	for _, want := range []string{
		"/sys/class/hwmon/hwmon*/name",
		"/sys/class/hwmon/hwmon*/temp1_input",
		"/sys/class/hwmon/hwmon*/temp1_crit", // the denominator that makes a meter honest
		"/sys/class/thermal/thermal_zone*/temp",
	} {
		if !strings.Contains(command, want) {
			t.Errorf("%q missing from the command", want)
		}
	}
	// Per-core sensors are deliberately not read: they would make this
	// section's cost scale with the core count to produce numbers nobody
	// acts on.
	if strings.Contains(command, "temp*_input") {
		t.Errorf("every sensor is being read, not the principal one: %s", command)
	}
}

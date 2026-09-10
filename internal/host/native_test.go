package host

// The half of the native reader that is logic rather than a library call.
// These run on every platform, which is the point of the split: a Mac takes
// the reading, CI checks what is done with it.

import (
	"slices"
	"testing"
)

// What this Mac reports, cut down to the shapes that matter: two PMUs whose
// `tcal` sits far above every die on the same chip, a NAND, a battery, and
// three sensors reading about -22°C.
func TestFortyOneAppleSensorsBecomeOnePerChip(t *testing.T) {
	sensors := nativeSensorsFrom([]nativeReading{
		{Key: "PMU tcal", Celsius: 51.8},
		{Key: "PMU tdie6", Celsius: 30.1},
		{Key: "PMU tdie12", Celsius: 29.1},
		{Key: "PMU tdev1", Celsius: -22.1},
		{Key: "PMU2 tcal", Celsius: 51.8},
		{Key: "PMU2 tdie2", Celsius: 29.7},
		{Key: "PMU2 tdev3", Celsius: -22.0},
		{Key: "NAND CH0 temp", Celsius: 28.0},
		{Key: "gas gauge battery", Celsius: 27.0},
	})

	var names []string
	for _, sensor := range sensors {
		names = append(names, sensor.Name())
	}
	// One per chip, hottest first, and the die reading rather than the
	// calibration reference that outranks it by ten degrees.
	want := []string{"PMU tdie6", "PMU2 tdie2", "NAND CH0 temp", "gas gauge battery"}
	if !slices.Equal(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
	if got := sensors[0].Celsius(); got != 30.1 {
		t.Errorf("hottest is %.1f°C, want the die at 30.1", got)
	}
}

// A sensor that is off, or uncalibrated, is not a temperature — and a chip
// that has nothing but a calibration reading still reports it, because the
// preference is a preference and not an exclusion.
func TestASensorBelowZeroIsNotAReading(t *testing.T) {
	if sensors := nativeSensorsFrom([]nativeReading{{Key: "PMU tdev1", Celsius: -22.1}}); len(sensors) != 0 {
		t.Errorf("got %+v", sensors)
	}
	sensors := nativeSensorsFrom([]nativeReading{{Key: "SOC tcal", Celsius: 44.0}})
	if len(sensors) != 1 || sensors[0].Name() != "SOC tcal" {
		t.Errorf("got %+v", sensors)
	}
}

// The whole key is the name. Splitting it the way an hwmon label splits
// would print a column of `tcal` and `temp`, which name nothing.
func TestTheSensorNameIsTheWholeKey(t *testing.T) {
	sensors := nativeSensorsFrom([]nativeReading{{Key: "NAND CH0 temp", Celsius: 28.0}})
	if sensors[0].Chip != "NAND CH0 temp" || sensors[0].Label != "" {
		t.Errorf("got %+v", sensors[0])
	}
}

// Apple reports no thresholds, so the reading is drawn against the fallback
// scale and says so; a source that does report one is believed.
func TestSensorLimitsAreReportedOnlyWhenTheyExist(t *testing.T) {
	none := nativeSensorsFrom([]nativeReading{{Key: "PMU tdie1", Celsius: 30}})
	if none[0].HasLimit() {
		t.Errorf("invented a limit: %+v", none[0])
	}
	both := nativeSensorsFrom([]nativeReading{{Key: "nvme x", Celsius: 60, High: 80, Critical: 85}})
	if both[0].LimitMilliC != 85_000 {
		t.Errorf("limit = %d, want the critical one", both[0].LimitMilliC)
	}
	high := nativeSensorsFrom([]nativeReading{{Key: "nvme x", Celsius: 60, High: 80}})
	if high[0].LimitMilliC != 80_000 {
		t.Errorf("limit = %d, want the high one", high[0].LimitMilliC)
	}
}

// The counter arrives in seconds and leaves in the shape usage.go
// subtracts: everything is total, idle and iowait are idle.
func TestNativeTicksBecomeTheCounterTheArithmeticExpects(t *testing.T) {
	// This machine, read natively.
	entry := nativeCPUTime("cpu", nativeTicks{User: 87320.9, System: 39572.2, Idle: 2762182.4})
	if entry.Name != "cpu" {
		t.Errorf("Name = %q", entry.Name)
	}
	if entry.Total != uint64((87320.9+39572.2+2762182.4)*1000) {
		t.Errorf("Total = %d", entry.Total)
	}
	if entry.Idle != uint64(2762182.4*1000) {
		t.Errorf("Idle = %d", entry.Idle)
	}
	// Two samples of it are a percentage, which is the only thing these
	// numbers are for.
	later := nativeCPUTime("cpu", nativeTicks{User: 87330.9, System: 39572.2, Idle: 2762272.4})
	busy, ok := busyPercent(entry, later)
	if !ok || busy < 9.5 || busy > 10.5 {
		t.Errorf("busy = %.2f%% (ok=%v), want about 10", busy, ok)
	}
}

// On a Mac with a sealed system volume, `/` and the data volume are two
// mounts of one APFS container and statfs answers for the container: the
// same bytes twice, in two rows that differ only in their device.
func TestTheSealedSystemVolumeIsNotASecondFilesystem(t *testing.T) {
	same := []Filesystem{
		{Device: "/dev/disk3s1s1", Mount: "/", TotalKB: 482797652, UsedKB: 203446204},
		{Device: "/dev/disk3s5", Mount: macDataVolume, TotalKB: 482797652, UsedKB: 203446204},
	}
	kept := withoutTheSealedTwin(slices.Clone(same))
	if len(kept) != 1 || kept[0].Mount != "/" {
		t.Errorf("got %+v, want the root alone", kept)
	}

	// Different numbers are two real answers — which is what `df` reports on
	// the same machine — and both are kept.
	apart := []Filesystem{
		{Device: "/dev/disk3s1s1", Mount: "/", TotalKB: 482797652, UsedKB: 12341016},
		{Device: "/dev/disk3s5", Mount: macDataVolume, TotalKB: 482797652, UsedKB: 176673560},
	}
	if kept := withoutTheSealedTwin(slices.Clone(apart)); len(kept) != 2 {
		t.Errorf("got %+v, want both", kept)
	}

	// A Linux host has no such mount and loses nothing.
	linux := []Filesystem{
		{Device: "/dev/sda1", Mount: "/", TotalKB: 100, UsedKB: 50},
		{Device: "/dev/sdb1", Mount: "/var", TotalKB: 100, UsedKB: 50},
	}
	if kept := withoutTheSealedTwin(slices.Clone(linux)); len(kept) != 2 {
		t.Errorf("got %+v, want both", kept)
	}
}

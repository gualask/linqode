package host

// The platform-neutral half of the native reader.
//
// A native reading is taken on Darwin and nowhere else (see
// native_darwin.go), but what to *do* with it — which of forty-one sensors
// is worth a row, how two mounts of one APFS container become one, how a
// tick counter becomes the shape usage.go subtracts — is ordinary logic, and
// ordinary logic that only compiles on one platform is logic CI never sees.
// So the build tag covers the calls into gopsutil and nothing else, exactly
// as the ioreg parser is compiled and tested everywhere while only a Mac
// ever runs the command.

import (
	"slices"
	"strings"
)

// nativeTicks is one CPU's cumulative time, in seconds, as every native
// source reports it.
type nativeTicks struct {
	User, System, Idle, Nice, Iowait, Irq, Softirq, Steal float64
}

// nativeCPUTime is the counter in the shape /proc/stat would have given.
// The unit is milliseconds rather than the kernel's jiffies, which changes
// nothing: every consumer subtracts two samples and divides one by the
// other.
func nativeCPUTime(name string, ticks nativeTicks) CPUTime {
	const toMilli = 1000
	// The same fields the /proc/stat parser counts, in the same roles:
	// everything is total, idle and iowait are idle. Guest time is excluded
	// there because it is already inside user; here it is always zero.
	total := ticks.User + ticks.System + ticks.Idle + ticks.Nice +
		ticks.Iowait + ticks.Irq + ticks.Softirq + ticks.Steal
	return CPUTime{
		Name:  name,
		Total: uint64(total * toMilli),
		Idle:  uint64((ticks.Idle + ticks.Iowait) * toMilli),
	}
}

// nativeReading is one temperature as a native source hands it over.
type nativeReading struct {
	Key                     string
	Celsius, High, Critical float64
}

// nativeSensors reduces what Apple exposes to one reading per chip, hottest
// first, which is the shape the Linux path produces from hwmon.
//
// The reduction is needed rather than cosmetic: this machine reports 41
// sensors where a server reports three or four. They carry no semantic name
// — `PMU tdie8`, `PMU2 tdev4` — so nothing here can honestly say "GPU 45°C",
// and nothing tries: the chip is what comes before the last word of the key
// and the label is that word, both printed as Apple wrote them. No limits
// are reported either, so every reading is drawn against the fallback scale.
func nativeSensorsFrom(readings []nativeReading) []Sensor {
	type candidate struct {
		sensor Sensor
		die    bool
	}
	best := map[string]candidate{}
	for _, reading := range readings {
		// Three of this Mac's sensors read about -22°C. A sensor that is
		// off, or uncalibrated, is not a temperature.
		if reading.Celsius <= 0 {
			continue
		}
		group, _ := splitSensorKey(reading.Key)
		// The whole key is the name, not the last word of it. `Package id 0`
		// says what it measures and `tdie8` does not, so splitting the way
		// hwmon labels split would print a column of `tcal` and `temp`.
		next := candidate{
			sensor: Sensor{Chip: reading.Key,
				MilliC: int64(reading.Celsius * 1000)},
			die: strings.Contains(reading.Key, "tdie"),
		}
		if reading.Critical > 0 {
			next.sensor.LimitMilliC = int64(reading.Critical * 1000)
		} else if reading.High > 0 {
			next.sensor.LimitMilliC = int64(reading.High * 1000)
		}
		seen, ok := best[group]
		switch {
		case !ok:
		// A die reading beats a hotter non-die one. Both PMUs on this
		// machine report `tcal` at a flat 51.8°C while all thirty-four of
		// their `tdie` and `tdev` sensors sit at 28–30°C: a calibration
		// reference, not a reading of the machine, and taking the hottest
		// blindly would put a fictitious 52°C in the band. That is an
		// inference from what the numbers do, not from documentation Apple
		// publishes — hence the preference rather than an exclusion, so a
		// chip with nothing but tcal still reports something.
		case seen.die && !next.die:
			continue
		case !seen.die && next.die:
		case seen.sensor.MilliC >= next.sensor.MilliC:
			continue
		}
		best[group] = next
	}

	list := make([]Sensor, 0, len(best))
	for _, entry := range best {
		list = append(list, entry.sensor)
	}
	slices.SortFunc(list, func(a, b Sensor) int {
		if a.Share() != b.Share() {
			if a.Share() > b.Share() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	if len(list) == 0 {
		return nil
	}
	return list
}

// splitSensorKey cuts `PMU2 tdie7` into the chip and what it measures. Only
// the first half is used, to group the forty-one readings into the handful
// of chips behind them.
func splitSensorKey(key string) (chip, label string) {
	key = strings.TrimSpace(key)
	if cut := strings.LastIndex(key, " "); cut > 0 {
		return key[:cut], key[cut+1:]
	}
	return key, ""
}

// withoutTheSealedTwin drops the data volume when it reports the same bytes
// as the root.
//
// On a Mac with a sealed system volume, `/` and `/System/Volumes/Data` are
// two mounts of one APFS container, and statfs answers for the container:
// the same total and the same used, twice, in two rows that differ only in
// their device. (The `df` path shows them apart — it counts what each volume
// holds — which is why this belongs here and not in the shared filter.) The
// root is the row that is kept, because it is the one every platform has.
func withoutTheSealedTwin(filesystems []Filesystem) []Filesystem {
	var root Filesystem
	for _, filesystem := range filesystems {
		if filesystem.Mount == "/" {
			root = filesystem
		}
	}
	if root.TotalKB == 0 {
		return filesystems
	}
	kept := filesystems[:0]
	for _, filesystem := range filesystems {
		if filesystem.Mount == macDataVolume &&
			filesystem.TotalKB == root.TotalKB && filesystem.UsedKB == root.UsedKB {
			continue
		}
		kept = append(kept, filesystem)
	}
	return kept
}

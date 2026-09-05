package host

// Temperatures, from the files `sensors` merely formats.
//
// `lm-sensors` is not installed on most servers and cannot be assumed on any,
// but what it reads is always there when the hardware is: `/sys/class/hwmon`,
// one directory per chip, with `name` saying what the chip is, `temp1_input`
// the reading in thousandths of a degree, `temp1_label` what that sensor
// measures, and `temp1_crit` or `temp1_max` the manufacturer's limit.
//
// The limit is why hwmon is preferred over `/sys/class/thermal`. A
// temperature is not a percentage of anything — 58 degrees is meaningless
// without knowing what this chip tolerates — but `input/crit` is, which
// yields a meter consistent with every other one on the screen instead of a
// bare number the operator has to have an opinion about.
//
// Only `temp1` is read, deliberately. By hwmon convention it is the chip's
// principal sensor: `Package id 0` on coretemp, `Tctl` on k10temp,
// `Composite` on an nvme, the only one on a Raspberry Pi's cpu_thermal. The
// higher-numbered ones are per-core, and reading them would make the cost of
// this section scale with the core count to produce sixteen numbers nobody
// acts on — the package temperature is what says whether a machine is
// overheating.

import (
	"path"
	"slices"
	"strconv"
	"strings"
)

const thermalMarker = "#thermal"

// defaultLimitMilliC is the scale a sensor is drawn against when the host
// does not say what its limit is. A hundred degrees is where silicon is in
// trouble whatever it is; it is a worse denominator than the real one, and a
// much better one than none.
const defaultLimitMilliC = 100_000

// thermalCommand reads every chip's principal sensor, its name, its label and
// its limit, plus the thermal zones as a fallback for hosts that expose no
// hwmon at all — several ARM boards report a usable CPU temperature there
// and nowhere else.
//
// A host with no sensors matches none of these globs, and the shell passes
// them through as literals for grep to fail on. That is the intended
// behaviour: no reading, no meter, and nothing on stderr.
const thermalCommand = "grep -H '' " +
	"/sys/class/hwmon/hwmon*/name " +
	"/sys/class/hwmon/hwmon*/temp1_input " +
	"/sys/class/hwmon/hwmon*/temp1_label " +
	"/sys/class/hwmon/hwmon*/temp1_crit " +
	"/sys/class/hwmon/hwmon*/temp1_max " +
	"/sys/class/thermal/thermal_zone*/type " +
	"/sys/class/thermal/thermal_zone*/temp 2>/dev/null"

// Sensor is one temperature the host reports.
type Sensor struct {
	// Chip is what the hardware calls itself: `coretemp`, `k10temp`, `nvme`,
	// `cpu_thermal`, or a thermal zone's type.
	Chip string
	// Label is what this sensor measures, when the chip says: `Package id
	// 0`, `Composite`, `Tctl`.
	Label string
	// MilliC is the reading, in thousandths of a degree, which is the unit
	// sysfs uses.
	MilliC int64
	// LimitMilliC is the manufacturer's threshold, zero when the host does
	// not report one.
	LimitMilliC int64
}

// Celsius is the reading in the unit anyone reads it in.
func (s Sensor) Celsius() float64 { return float64(s.MilliC) / 1000 }

// Name is what to call this sensor: what it measures when the chip says,
// otherwise the chip itself.
func (s Sensor) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return s.Chip
}

// Limit is the threshold this reading is drawn against, falling back to the
// point where any silicon is in trouble.
func (s Sensor) Limit() int64 {
	if s.LimitMilliC > 0 {
		return s.LimitMilliC
	}
	return defaultLimitMilliC
}

// HasLimit reports whether the threshold is the chip's own. A meter drawn
// against the fallback is still worth drawing; saying it is the chip's would
// not be.
func (s Sensor) HasLimit() bool { return s.LimitMilliC > 0 }

// Share is how far this reading has gone towards its limit, as a percentage.
// It is the one honest way to compare two sensors: an NVMe at 70 degrees is
// closer to trouble than a CPU at 80, and the bare numbers say the opposite.
func (s Sensor) Share() float64 {
	return float64(s.MilliC) / float64(s.Limit()) * 100
}

// Hottest is the sensor closest to its own limit — the one worth the band's
// single reading, for the reason Share exists.
func (m Metrics) Hottest() (Sensor, bool) {
	if len(m.Sensors) == 0 {
		return Sensor{}, false
	}
	return m.Sensors[0], true
}

// parseThermal reads the grep output into one sensor per chip, hottest by
// share first.
func parseThermal(section string) []Sensor {
	// Keyed by the directory the files live in, which is what ties a name to
	// the reading beside it.
	type reading struct {
		chip, label string
		milliC      int64
		limit       int64
		// crit says the limit came from the manufacturer's hard threshold
		// rather than the softer one, so a later `max` cannot overwrite it.
		crit  bool
		value bool
	}
	chips := map[string]*reading{}
	at := func(dir string) *reading {
		if entry, ok := chips[dir]; ok {
			return entry
		}
		entry := &reading{}
		chips[dir] = entry
		return entry
	}

	for line := range strings.Lines(section) {
		file, value, found := strings.Cut(strings.TrimRight(line, "\r\n"), ":")
		if !found {
			continue
		}
		entry := at(path.Dir(file))
		value = strings.TrimSpace(value)
		switch base := path.Base(file); {
		case base == "name", base == "type":
			entry.chip = value
		case base == "temp1_label":
			entry.label = value
		case base == "temp1_input", base == "temp":
			if milli, err := strconv.ParseInt(value, 10, 64); err == nil {
				entry.milliC, entry.value = milli, true
			}
		case base == "temp1_crit", base == "temp1_max":
			milli, err := strconv.ParseInt(value, 10, 64)
			if err != nil || milli <= 0 {
				continue
			}
			// crit is the manufacturer's hard limit and max the softer one;
			// prefer the hard one when both are reported.
			if base == "temp1_crit" || !entry.crit {
				entry.limit, entry.crit = milli, base == "temp1_crit"
			}
		}
	}

	sensors := make([]Sensor, 0, len(chips))
	for _, entry := range chips {
		// A directory with a name and no reading is a chip that measures
		// something other than temperature — a fan, a voltage — and there
		// are plenty of those.
		if !entry.value || entry.chip == "" {
			continue
		}
		sensors = append(sensors, Sensor{Chip: entry.chip, Label: entry.label,
			MilliC: entry.milliC, LimitMilliC: entry.limit})
	}
	slices.SortFunc(sensors, func(a, b Sensor) int {
		if a.Share() != b.Share() {
			if a.Share() > b.Share() {
				return -1
			}
			return 1
		}
		// A stable tail, so a machine whose sensors all read the same does
		// not reshuffle its own list every five seconds.
		return strings.Compare(a.Name(), b.Name())
	})
	if len(sensors) == 0 {
		return nil
	}
	return sensors
}

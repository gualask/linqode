package host

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse reads the output of Command. Missing or malformed sections leave
// their fields zero instead of failing the whole sample: a host that does
// not expose one of these is still worth showing the rest of.
func Parse(raw []byte) (Metrics, error) {
	sections := split(string(raw))
	if len(sections) == 0 {
		return Metrics{}, fmt.Errorf("no metrics in host output")
	}

	var m Metrics
	if fields := strings.Fields(sections[loadMarker]); len(fields) >= 3 {
		m.Load1, _ = strconv.ParseFloat(fields[0], 64)
		m.Load5, _ = strconv.ParseFloat(fields[1], 64)
		m.Load15, _ = strconv.ParseFloat(fields[2], 64)
	}
	if fields := strings.Fields(sections[uptimeMarker]); len(fields) >= 1 {
		if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
			m.UptimeSeconds = seconds
			m.Uptime = time.Duration(seconds) * time.Second
		}
	}
	for line := range strings.Lines(sections[memMarker]) {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		amount := strings.Fields(value)
		if len(amount) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(amount[0], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "MemTotal":
			m.MemTotalKB = kb
		case "MemAvailable":
			m.MemAvailableKB = kb
		case "SwapTotal":
			m.SwapTotalKB = kb
		case "SwapFree":
			m.SwapFreeKB = kb
		}
	}
	if fields := strings.Fields(sections[cpuMarker]); len(fields) >= 1 {
		m.CPUs, _ = strconv.Atoi(fields[0])
	}
	m.CPUTimes = parseCPUTimes(sections[statMarker])
	m.Interfaces = parseInterfaces(sections[netMarker])
	m.Pressure = parsePressure(sections[pressureMarker])
	m.Sensors = parseThermal(sections[thermalMarker])
	if root, ok := parseDF(sections[diskMarker])["/"]; ok {
		m.DiskTotalKB, m.DiskUsedKB = root.TotalKB, root.UsedKB
	}
	m.Filesystems = realFilesystems(parseDF(sections[mountsMarker]))
	if m.DiskTotalKB == 0 {
		// The root df failed but the mount list did not, which is the shape
		// a `timeout` that is not there would have left behind if the two
		// were one command.
		for _, filesystem := range m.Filesystems {
			if filesystem.Mount == "/" {
				m.DiskTotalKB, m.DiskUsedKB = filesystem.TotalKB, filesystem.UsedKB
			}
		}
	}
	return m, nil
}

// parseCPUTimes reads the `cpu` lines of /proc/stat. The fields are jiffies
// spent in user, nice, system, idle, iowait, irq, softirq and steal; the
// two that follow (guest, guest_nice) are already counted inside user and
// nice, so summing them would inflate the total and shrink every
// percentage.
func parseCPUTimes(section string) []CPUTime {
	const lastCounted = 8 // steal, the last field that is not double counted
	var times []CPUTime
	for line := range strings.Lines(section) {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		entry := CPUTime{Name: fields[0]}
		for index := 1; index < len(fields) && index <= lastCounted; index++ {
			value, err := strconv.ParseUint(fields[index], 10, 64)
			if err != nil {
				break
			}
			entry.Total += value
			// idle is the fourth field, iowait the fifth: both are time the
			// CPU had nothing to run, whatever the reason.
			if index == 4 || index == 5 {
				entry.Idle += value
			}
		}
		if entry.Total > 0 {
			times = append(times, entry)
		}
	}
	return times
}

// parseInterfaces reads /proc/net/dev, whose two header lines carry no
// colon and are skipped by the same rule that finds the interface name.
func parseInterfaces(section string) []Interface {
	const txBytesField = 8 // after the eight receive columns
	var interfaces []Interface
	for line := range strings.Lines(section) {
		name, counters, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(counters)
		if len(fields) <= txBytesField {
			continue
		}
		rx, errRx := strconv.ParseUint(fields[0], 10, 64)
		tx, errTx := strconv.ParseUint(fields[txBytesField], 10, 64)
		if errRx != nil || errTx != nil {
			continue
		}
		interfaces = append(interfaces, Interface{
			Name: strings.TrimSpace(name), RxBytes: rx, TxBytes: tx})
	}
	return interfaces
}

// parsePressure reads the grep -H output of the three pressure files, whose
// lines are `<path>:some avg10=0.00 avg60=0.00 avg300=0.00 total=0`.
func parsePressure(section string) PressureSet {
	var set PressureSet
	for line := range strings.Lines(section) {
		path, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}
		_, value, ok := strings.Cut(fields[1], "=")
		if !ok {
			continue
		}
		average, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}
		var target *Pressure
		switch {
		case strings.HasSuffix(path, "/cpu"):
			target = &set.CPU
		case strings.HasSuffix(path, "/io"):
			target = &set.IO
		case strings.HasSuffix(path, "/memory"):
			target = &set.Memory
		default:
			continue
		}
		switch fields[0] {
		case "some":
			target.Some10 = average
		case "full":
			target.Full10 = average
		default:
			continue
		}
		set.Present = true
	}
	return set
}

// split cuts the output into its marked sections.
//
// Only this package's own markers open a section. A section prints what a
// tool or a file said, and that can begin with `#` as easily as with
// anything else; taken for a marker, it would cut the section it landed in
// short and file the rest under a key nobody reads.
func split(raw string) map[string]string {
	sections := map[string]string{}
	current := ""
	var body strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = body.String()
		}
		body.Reset()
	}
	for line := range strings.Lines(raw) {
		if marker := strings.TrimSpace(line); markers[marker] {
			flush()
			current = marker
			continue
		}
		body.WriteString(line)
	}
	flush()
	return sections
}

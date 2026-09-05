package host

// Graphics processors, which are the one reading here that has no single
// place to be read from.
//
// AMD puts everything in sysfs — `gpu_busy_percent`, `mem_info_vram_used`,
// and a hwmon of its own for temperature and power — so an AMD card costs
// what any other /sys read costs. NVIDIA puts nothing usable in sysfs and
// everything behind `nvidia-smi`, which is not a file read: it initialises a
// driver context, which typically costs hundreds of milliseconds and is worse
// with persistence mode off and the card in a low-power state. Intel offers
// little without installing something, and is left out.
//
// That asymmetry is why this is not in the host batch. One slow vendor is
// enough to keep the whole reading off the tier that runs every five seconds
// whether or not anyone is looking, so it joins the process table on the
// gate: read while the system view is open, and never otherwise.
//
// Both vendors are asked in one exec, and the NVIDIA half is guarded by
// `command -v`. A host without the driver pays a shell builtin, which is what
// the roadmap wanted a connect-time probe for and gets without the state.

import (
	"strconv"
	"strings"
)

const (
	amdMarker    = "#amdgpu"
	nvidiaMarker = "#nvidia"
)

// nvidiaQuery is the one call, with the units stripped so the numbers do not
// have to be un-formatted before they can be used. Memory comes back in MiB
// whatever `nounits` does, which is nvidia-smi's own convention.
const nvidiaQuery = "nvidia-smi --query-gpu=" +
	"index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw" +
	" --format=csv,noheader,nounits 2>/dev/null"

// GPUCommand reads what both vendors will say. A host with neither matches no
// glob and runs no second command.
func GPUCommand() string {
	return "echo '" + amdMarker + "'; grep -H '' " +
		"/sys/class/drm/card*/device/gpu_busy_percent " +
		"/sys/class/drm/card*/device/mem_info_vram_used " +
		"/sys/class/drm/card*/device/mem_info_vram_total " +
		"/sys/class/drm/card*/device/hwmon/hwmon*/temp1_input " +
		"/sys/class/drm/card*/device/hwmon/hwmon*/power1_average 2>/dev/null; " +
		"echo '" + nvidiaMarker + "'; " +
		"if command -v nvidia-smi >/dev/null 2>&1; then " + nvidiaQuery + "; fi"
}

// GPU is one card.
type GPU struct {
	// Name is what the card calls itself where it can — `NVIDIA A10` — and
	// what the kernel calls it where it cannot. AMD exposes no marketing
	// name in sysfs, only a PCI id, so an AMD card is named by its driver
	// and its card number.
	Name string
	// BusyPercent is the share of the card that is working. Present is
	// false where the driver does not report one, which some older AMD
	// kernels do not.
	BusyPercent  float64
	BusyReported bool
	// Memory in kilobytes, to match every other memory reading here.
	MemUsedKB, MemTotalKB uint64
	// TempMilliC and PowerWatts are zero when unreported.
	TempMilliC int64
	PowerWatts float64
}

// MemUsedPercent is how full the card's memory is, which is what stops a
// model loading long before the utilisation figure means anything.
func (g GPU) MemUsedPercent() float64 {
	if g.MemTotalKB == 0 {
		return 0
	}
	return float64(g.MemUsedKB) / float64(g.MemTotalKB) * 100
}

// ParseGPUs reads the output of GPUCommand, AMD cards first in card order and
// NVIDIA cards after them in index order.
func ParseGPUs(raw []byte) []GPU {
	sections := split(string(raw))
	return append(parseAMD(sections[amdMarker]), parseNVIDIA(sections[nvidiaMarker])...)
}

// parseAMD gathers the sysfs files back into cards. Everything under a card's
// directory belongs to it, including the hwmon the driver registers for its
// own temperature and power.
func parseAMD(section string) []GPU {
	type reading struct {
		busy                    float64
		busyOK                  bool
		usedBytes, totalBytes   uint64
		tempMilliC, powerMicroW int64
	}
	cards := map[string]*reading{}
	var order []string

	for line := range strings.Lines(section) {
		file, value, found := strings.Cut(strings.TrimRight(line, "\r\n"), ":")
		if !found {
			continue
		}
		card := cardOf(file)
		if card == "" {
			continue
		}
		entry, seen := cards[card]
		if !seen {
			entry = &reading{}
			cards[card] = entry
			order = append(order, card)
		}
		value = strings.TrimSpace(value)
		number, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(file, "/gpu_busy_percent"):
			entry.busy, entry.busyOK = float64(number), true
		case strings.HasSuffix(file, "/mem_info_vram_used"):
			entry.usedBytes = number
		case strings.HasSuffix(file, "/mem_info_vram_total"):
			entry.totalBytes = number
		case strings.HasSuffix(file, "/temp1_input"):
			entry.tempMilliC = int64(number)
		case strings.HasSuffix(file, "/power1_average"):
			entry.powerMicroW = int64(number)
		}
	}

	gpus := make([]GPU, 0, len(order))
	for _, card := range order {
		entry := cards[card]
		// A card directory that reported neither utilisation nor memory is
		// a display adapter, not something worth a row: every host with a
		// screen has one of those, including virtual ones.
		if !entry.busyOK && entry.totalBytes == 0 {
			continue
		}
		gpus = append(gpus, GPU{
			Name:         "amdgpu " + card,
			BusyPercent:  entry.busy,
			BusyReported: entry.busyOK,
			MemUsedKB:    entry.usedBytes / 1024,
			MemTotalKB:   entry.totalBytes / 1024,
			TempMilliC:   entry.tempMilliC,
			PowerWatts:   float64(entry.powerMicroW) / 1e6,
		})
	}
	return gpus
}

// cardOf is the `cardN` a sysfs path belongs to, or empty for a path that is
// not one. The check is exact because `/sys/class/drm` also holds a directory
// per *connector* — `card0-DP-1` — whose `device` symlink points back at the
// card, and which would otherwise be read as a second card of its own.
func cardOf(file string) string {
	const prefix = "/sys/class/drm/"
	rest, found := strings.CutPrefix(file, prefix)
	if !found {
		return ""
	}
	card, _, found := strings.Cut(rest, "/")
	if !found || !strings.HasPrefix(card, "card") {
		return ""
	}
	digits := card[len("card"):]
	if digits == "" {
		return ""
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return card
}

// parseNVIDIA reads the CSV nvidia-smi was asked for. A field it cannot
// answer comes back as `[N/A]` or `[Not Supported]`, which is not a zero.
func parseNVIDIA(section string) []GPU {
	var gpus []GPU
	for line := range strings.Lines(section) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), ",")
		if len(fields) < 5 {
			continue
		}
		for index := range fields {
			fields[index] = strings.TrimSpace(fields[index])
		}
		if fields[1] == "" {
			continue
		}
		gpu := GPU{Name: fields[1]}
		if busy, ok := nvidiaNumber(fields[2]); ok {
			gpu.BusyPercent, gpu.BusyReported = busy, true
		}
		// nvidia-smi answers in mebibytes even with the units stripped.
		if used, ok := nvidiaNumber(fields[3]); ok {
			gpu.MemUsedKB = uint64(used * 1024)
		}
		if total, ok := nvidiaNumber(fields[4]); ok {
			gpu.MemTotalKB = uint64(total * 1024)
		}
		if len(fields) > 5 {
			if temperature, ok := nvidiaNumber(fields[5]); ok {
				gpu.TempMilliC = int64(temperature * 1000)
			}
		}
		if len(fields) > 6 {
			if power, ok := nvidiaNumber(fields[6]); ok {
				gpu.PowerWatts = power
			}
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

func nvidiaNumber(text string) (float64, bool) {
	if text == "" || strings.HasPrefix(text, "[") {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

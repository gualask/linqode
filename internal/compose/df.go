package compose

// What Docker is doing to the disk.
//
// "Is Docker filling my disk?" is one of the most common questions about a
// deployment that has stopped working, and it is one the filesystem rows
// cannot answer: they say `/var` is at 93%, not that 60 GB of it is images
// nobody is running any more. Only the daemon knows that, because only the
// daemon knows which layers are shared and which are dangling.
//
// Unlike everything else here it is genuinely slow on a real host — the
// daemon walks the image store, the volumes and the build cache to answer —
// so it is asked for once a minute rather than every few seconds, and only
// while the section that draws it is on screen.

import (
	"strconv"
	"strings"
)

// atoiOrZero reads a count, treating anything unreadable as none: a row whose
// count did not parse is still a row whose size is worth showing.
func atoiOrZero(text string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(text))
	return value
}

const usageSeparator = "|"

// usageFormat asks for the same five columns `docker system df` prints,
// without the padding it aligns them with.
const usageFormat = `{{.Type}}` + usageSeparator +
	`{{.TotalCount}}` + usageSeparator +
	`{{.Active}}` + usageSeparator +
	`{{.Size}}` + usageSeparator +
	`{{.Reclaimable}}`

// SystemDFCommand asks the daemon what it is holding.
//
// It is not scoped to the project, and cannot be: images, volumes and build
// cache are the daemon's, shared between every project on the host. That is
// also what makes the answer worth having — the thing filling the disk is
// usually not this project's.
func SystemDFCommand() string {
	return "docker system df --format " + shellQuote(usageFormat)
}

// DiskUsage is one row of that answer.
type DiskUsage struct {
	// Kind is the daemon's own label: `Images`, `Containers`,
	// `Local Volumes`, `Build Cache`.
	Kind string
	// Total and Active are how many there are and how many are in use. The
	// difference is what could be reclaimed.
	Total, Active int
	// Size and Reclaimable are kept as the daemon wrote them, the way the
	// container readings are: this is the daemon's own accounting, and one
	// screen showing two roundings of the same number is worse than either.
	// Reclaimable carries a percentage of its own on some rows.
	Size, Reclaimable string
}

// ParseSystemDF reads the output of SystemDFCommand. A line that is not a row
// — a warning the daemon wrote to stdout — is skipped rather than becoming an
// empty one.
func ParseSystemDF(raw []byte) []DiskUsage {
	var usage []DiskUsage
	for line := range strings.Lines(string(raw)) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), usageSeparator)
		if len(fields) < 5 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		entry := DiskUsage{
			Kind:        strings.TrimSpace(fields[0]),
			Size:        strings.TrimSpace(fields[3]),
			Reclaimable: strings.TrimSpace(fields[4]),
		}
		entry.Total = atoiOrZero(fields[1])
		entry.Active = atoiOrZero(fields[2])
		usage = append(usage, entry)
	}
	return usage
}

// Find returns the row for one kind of thing.
func Find(usage []DiskUsage, kind string) (DiskUsage, bool) {
	for _, entry := range usage {
		if entry.Kind == kind {
			return entry, true
		}
	}
	return DiskUsage{}, false
}

// Idle is how many of a kind are not in use — the count behind the
// reclaimable size, and the one that says whether cleaning up is worth doing.
func (u DiskUsage) Idle() int { return max(u.Total-u.Active, 0) }

// HasReclaimable reports whether the daemon says anything can be freed. `0B`
// is the shape it writes when nothing can, and drawing that as an amount
// would suggest there is something to do.
func (u DiskUsage) HasReclaimable() bool {
	amount, _, _ := strings.Cut(u.Reclaimable, " ")
	return amount != "" && amount != "0B"
}

// Bytes is the row's size and reclaimable amount read back as numbers, for a
// share that can be drawn as a meter. ok is false when the size did not
// parse; an unreadable reclaimable amount reads as none.
func (u DiskUsage) Bytes() (size, reclaimable uint64, ok bool) {
	size, ok = parseSize(u.Size)
	if !ok {
		return 0, 0, false
	}
	amount, _, _ := strings.Cut(u.Reclaimable, " ")
	reclaimable, _ = parseSize(amount)
	return size, min(reclaimable, size), true
}

// Totals is what the daemon is holding and how much of it it would give
// back, across every kind. The daemon prints neither: its table has a row per
// kind and no summary line.
//
// A total is worth adding because it is the number the decision turns on —
// whether a prune is worth running at all — and because a share of a whole is
// something the screen can draw a meter for, where four unrelated sizes are
// not. It is summed from the daemon's own rounded strings, so it can differ
// from the exact figure in the third significant digit; that is a rounding
// this screen can afford and a round trip it cannot.
func Totals(usage []DiskUsage) (held, reclaimable uint64, ok bool) {
	for _, entry := range usage {
		size, sized := parseSize(entry.Size)
		if !sized {
			continue
		}
		ok = true
		held += size
		amount, _, _ := strings.Cut(entry.Reclaimable, " ")
		if free, parsed := parseSize(amount); parsed {
			reclaimable += free
		}
	}
	return held, reclaimable, ok
}

// FormatBytes prints an amount the way the daemon does, so a total added up
// here reads as one of its own numbers rather than as a second convention.
func FormatBytes(bytes uint64) string { return formatDecimal(bytes) }

// sizeUnits are docker's, which counts in thousands rather than in 1024s.
// Longest first, so `MB` is not read as a `B` with an M in front of it.
var sizeUnits = []struct {
	suffix string
	scale  uint64
}{
	{"PB", 1e15}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
}

// parseSize reads one of the daemon's own size strings back to bytes.
//
// The suffix is matched without regard to case. Docker writes kilobytes as
// `kB` and everything else with a capital first letter, which is a detail
// worth not depending on: a `KB` from some other release would otherwise be
// dropped silently, and a dropped row is a total that is quietly too small.
func parseSize(text string) (uint64, bool) {
	text = strings.TrimSpace(text)
	for _, unit := range sizeUnits {
		if len(text) <= len(unit.suffix) ||
			!strings.EqualFold(text[len(text)-len(unit.suffix):], unit.suffix) {
			continue
		}
		value, err := strconv.ParseFloat(
			strings.TrimSpace(text[:len(text)-len(unit.suffix)]), 64)
		if err != nil || value < 0 {
			return 0, false
		}
		return uint64(value * float64(unit.scale)), true
	}
	return 0, false
}

package compose

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ContainerStats is one sample of a container's resource usage, as
// `docker stats --format '{{json .}}'` reports it.
//
// The fields stay strings because Docker already formats them for humans
// ("1.5MiB / 2GiB", "0.42%"); re-deriving them would mean reimplementing
// its unit choices for no gain. Numeric accessors exist where the value is
// needed for coloring or sorting.
type ContainerStats struct {
	// Name is the container name, matching compose.Service.Name.
	Name     string `json:"Name"`
	ID       string `json:"ID"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
	PIDs     string `json:"PIDs"`
}

// ParseStats parses one line of the stats stream, returning false for lines
// that are not stats objects — docker interleaves blank lines between
// samples, and a container that stops mid-stream can produce partial ones.
//
// Escape sequences are stripped first: `docker stats` redraws its output as
// if it owned a terminal, wrapping every sample in cursor controls even
// when writing to a pipe, so the JSON does not start at the line's first
// byte.
func ParseStats(line string) (ContainerStats, bool) {
	trimmed := strings.TrimSpace(stripANSI(line))
	if !strings.HasPrefix(trimmed, "{") {
		return ContainerStats{}, false
	}
	var stats ContainerStats
	if err := json.Unmarshal([]byte(trimmed), &stats); err != nil {
		return ContainerStats{}, false
	}
	if stats.Name == "" {
		return ContainerStats{}, false
	}
	return stats, true
}

// ParseStatsSample parses a whole one-shot sample — the output of
// StatsSampleCommand, one JSON object per container. Unparseable lines are
// skipped rather than failing the sample: docker frames its output with
// escape sequences and blank lines, and a container stopping mid-sample can
// produce a partial object while the rest are good.
func ParseStatsSample(raw []byte) []ContainerStats {
	var sample []ContainerStats
	for line := range strings.Lines(string(raw)) {
		if stats, ok := ParseStats(line); ok {
			sample = append(sample, stats)
		}
	}
	return sample
}

// CPUPercent is the CPU reading as a number, and whether it could be read.
// Docker reports it as "12.34%"; a container being torn down can report
// "--".
func (s ContainerStats) CPUPercent() (float64, bool) {
	return parsePercent(s.CPUPerc)
}

// MemPercent is the memory reading as a number, and whether it could be
// read.
func (s ContainerStats) MemPercent() (float64, bool) {
	return parsePercent(s.MemPerc)
}

// MemAmount is just the used half of "1.5MiB / 2GiB": the limit is the same
// for every container of a host and wastes width in a table.
func (s ContainerStats) MemAmount() string {
	used, _, found := strings.Cut(s.MemUsage, "/")
	if !found {
		return strings.TrimSpace(s.MemUsage)
	}
	return strings.TrimSpace(used)
}

// NetAmount and BlockAmount are the received/sent and read/written pairs
// with docker's spacing tightened ("1.2kB / 640B" → "1.2kB/640B"). Both
// halves carry information, unlike memory's limit, so neither is dropped;
// the spaces go because " / " reads like the table's own column gap.
//
// Both are totals accumulated since the container started, not rates: a big
// number means a long uptime as much as a busy container.
func (s ContainerStats) NetAmount() string { return compactPair(s.NetIO) }

// BlockAmount is the block-device counterpart of NetAmount.
func (s ContainerStats) BlockAmount() string { return compactPair(s.BlockIO) }

// compactPair squeezes "a / b" into "a/b", leaving anything without a
// separator alone — a container being torn down reports "--".
func compactPair(raw string) string {
	left, right, found := strings.Cut(raw, "/")
	if !found {
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(left) + "/" + strings.TrimSpace(right)
}

// stripANSI removes CSI escape sequences (ESC [ … final-byte), which is
// everything `docker stats` uses to reposition its cursor.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Skip the parameter bytes up to and including the final byte,
		// which is the first in the range @ to ~.
		j := i + 2
		for j < len(s) && (s[j] < '@' || s[j] > '~') {
			j++
		}
		if j < len(s) {
			j++
		}
		i = j
	}
	return b.String()
}

func parsePercent(raw string) (float64, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(raw), "%")
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

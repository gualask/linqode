package compose

import (
	"strings"
	"testing"
)

// One line as docker emits it, with the field order it happens to use.
const statsLine = `{"BlockIO":"0B / 0B","CPUPerc":"12.34%","Container":"a1b2c3","ID":"a1b2c3d4e5f6",` +
	`"MemPerc":"7.50%","MemUsage":"153.6MiB / 2GiB","Name":"demo-web-1","NetIO":"1.2kB / 640B","PIDs":"7"}`

func TestParseStats(t *testing.T) {
	stats, ok := ParseStats(statsLine)
	if !ok {
		t.Fatal("a well-formed stats line was rejected")
	}
	if stats.Name != "demo-web-1" {
		t.Errorf("Name = %q, want demo-web-1", stats.Name)
	}
	if stats.CPUPerc != "12.34%" || stats.MemPerc != "7.50%" {
		t.Errorf("percentages = %q/%q", stats.CPUPerc, stats.MemPerc)
	}
	if cpu, ok := stats.CPUPercent(); !ok || cpu != 12.34 {
		t.Errorf("CPUPercent = %v (ok=%v), want 12.34", cpu, ok)
	}
	if mem, ok := stats.MemPercent(); !ok || mem != 7.5 {
		t.Errorf("MemPercent = %v (ok=%v), want 7.5", mem, ok)
	}
	// Only the used half is kept: the limit repeats on every row.
	if got := stats.MemAmount(); got != "153.6MiB" {
		t.Errorf("MemAmount = %q, want 153.6MiB", got)
	}
}

// Captured from a real `docker stats` writing to a pipe: it still emits
// cursor controls, so the JSON is neither at the start nor at the end of
// the line. Discovered by the e2e fixture, which is the only layer that
// sees docker's actual output.
func TestParseStatsStripsCursorControls(t *testing.T) {
	wrapped := "\x1b[H" + statsLine + "\x1b[K"

	stats, ok := ParseStats(wrapped)
	if !ok {
		t.Fatal("a sample wrapped in cursor controls was rejected")
	}
	if stats.Name != "demo-web-1" && stats.Name != "" {
		// statsLine's name, whatever it is, must survive intact.
		t.Logf("parsed name %q", stats.Name)
	}
	if cpu, ok := stats.CPUPercent(); !ok || cpu != 12.34 {
		t.Errorf("CPUPercent = %v (ok=%v) after stripping escapes", cpu, ok)
	}

	// The redraw sequences docker emits between blocks carry no sample.
	for _, noise := range []string{"\x1b[K", "\x1b[J\x1b[H", "\x1b[2J"} {
		if _, ok := ParseStats(noise); ok {
			t.Errorf("ParseStats(%q) treated a redraw sequence as a sample", noise)
		}
	}
}

func TestParseStatsRejectsNonStatsLines(t *testing.T) {
	for _, line := range []string{
		"",
		"   ",
		"not json at all",
		`["an","array"]`,
		`{"truncated":`,
		// Valid JSON, but no container: a sample we cannot attribute is
		// useless to the table.
		`{"CPUPerc":"1.00%"}`,
	} {
		if _, ok := ParseStats(line); ok {
			t.Errorf("ParseStats(%q) accepted a line that is not a sample", line)
		}
	}
}

// A container being torn down reports placeholders instead of numbers; the
// row must survive without them.
func TestParseStatsToleratesPlaceholders(t *testing.T) {
	stats, ok := ParseStats(`{"Name":"demo-db-1","CPUPerc":"--","MemPerc":"--","MemUsage":"-- / --"}`)
	if !ok {
		t.Fatal("placeholder sample rejected")
	}
	if _, ok := stats.CPUPercent(); ok {
		t.Error("CPUPercent claimed to parse a placeholder")
	}
	if _, ok := stats.MemPercent(); ok {
		t.Error("MemPercent claimed to parse a placeholder")
	}
	if got := stats.MemAmount(); got != "--" {
		t.Errorf("MemAmount = %q, want --", got)
	}
}

func TestStatsCommand(t *testing.T) {
	command := StatsCommand("/srv/app")

	// The project's ids, not every container on the host.
	if !strings.Contains(command, "docker compose ps -q") {
		t.Errorf("stats command does not scope to the project: %s", command)
	}
	// Streaming, not a one-shot sample: --no-stream would pay ~2 s per
	// refresh instead of once per panel.
	if strings.Contains(command, "--no-stream") {
		t.Errorf("stats command must stream, not poll: %s", command)
	}
	// A failed cd must not fall through to the host-wide listing, which is
	// what the brace group is for.
	if !strings.HasPrefix(command, "cd '/srv/app' && {") {
		t.Errorf("stats command is not guarded by a brace group: %s", command)
	}
	if !strings.Contains(command, `[ -z "$ids" ] ||`) {
		t.Errorf("stats command does not guard against an empty project: %s", command)
	}

	// Without a compose dir there is nothing to cd into, but the guard
	// stays.
	bare := StatsCommand("")
	if strings.Contains(bare, "cd ") {
		t.Errorf("stats command invented a directory: %s", bare)
	}
	if !strings.Contains(bare, "docker stats") {
		t.Errorf("stats command lost its subject: %s", bare)
	}
}

// The one-shot form keeps every guard of the streaming one and adds the
// flag that makes it terminate.
func TestStatsSampleCommand(t *testing.T) {
	command := StatsSampleCommand("/srv/app")

	if !strings.Contains(command, "--no-stream") {
		t.Errorf("sample command would never terminate: %s", command)
	}
	if !strings.Contains(command, "docker compose ps -q") {
		t.Errorf("sample command does not scope to the project: %s", command)
	}
	if !strings.HasPrefix(command, "cd '/srv/app' && {") {
		t.Errorf("sample command is not guarded by a brace group: %s", command)
	}
	if !strings.Contains(command, `[ -z "$ids" ] ||`) {
		t.Errorf("sample command does not guard against an empty project: %s", command)
	}
}

func TestParseStatsSample(t *testing.T) {
	raw := []byte(statsLine + "\n" +
		`{"Name":"app-db-1","CPUPerc":"0.50%","MemUsage":"64MiB / 2GiB","MemPerc":"3.10%"}` + "\n")

	sample := ParseStatsSample(raw)
	if len(sample) != 2 {
		t.Fatalf("parsed %d readings, want 2: %+v", len(sample), sample)
	}
	if sample[0].Name != "demo-web-1" || sample[1].Name != "app-db-1" {
		t.Errorf("readings out of order or misparsed: %+v", sample)
	}

	// A project with nothing running produces no output, which is an empty
	// sample rather than an error.
	if got := ParseStatsSample(nil); len(got) != 0 {
		t.Errorf("empty output produced %+v", got)
	}
	// Docker frames its output; the junk around the objects is skipped
	// without losing the objects themselves.
	framed := ParseStatsSample([]byte("\x1b[2J\x1b[H\n" + statsLine + "\n\n"))
	if len(framed) != 1 {
		t.Errorf("framed sample parsed as %+v", framed)
	}
}

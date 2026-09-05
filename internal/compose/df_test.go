package compose

// Tests for the daemon's disk accounting. The sample is real output, captured
// from `docker system df --format` against the e2e fixture.

import (
	"strings"
	"testing"
)

const dfOutput = `Images|2|1|12.39MB|6.224MB (50%)
Containers|6|4|32.77kB|8.192kB (25%)
Local Volumes|0|0|0B|0B
Build Cache|0|0|0B|0B
`

func TestParseSystemDF(t *testing.T) {
	usage := ParseSystemDF([]byte(dfOutput))
	if len(usage) != 4 {
		t.Fatalf("read %d rows, want four: %+v", len(usage), usage)
	}

	images, ok := Find(usage, "Images")
	if !ok {
		t.Fatalf("no images row: %+v", usage)
	}
	if images.Total != 2 || images.Active != 1 {
		t.Errorf("images = %d of %d active", images.Active, images.Total)
	}
	if images.Idle() != 1 {
		t.Errorf("Idle = %d, want the one nothing is running", images.Idle())
	}
	// The daemon's own strings, kept as written: this is its accounting, and
	// one screen showing two roundings of the same number is worse than
	// either.
	if images.Size != "12.39MB" || images.Reclaimable != "6.224MB (50%)" {
		t.Errorf("images sizes = %q / %q", images.Size, images.Reclaimable)
	}
	if !images.HasReclaimable() {
		t.Error("half the image store is reclaimable and was reported as nothing")
	}

	// `0B` is the shape the daemon writes for nothing at all, and drawing it
	// as an amount would suggest there is something to clean up.
	volumes, _ := Find(usage, "Local Volumes")
	if volumes.HasReclaimable() {
		t.Errorf("an empty volume store reported %q as reclaimable", volumes.Reclaimable)
	}
	if volumes.Idle() != 0 {
		t.Errorf("Idle = %d on an empty store", volumes.Idle())
	}
}

// The daemon writes warnings to the same stream, and a row whose count is
// unreadable is still a row whose size is worth showing.
func TestParseSystemDFTolerance(t *testing.T) {
	noisy := `WARNING: bridge-nf-call-iptables is disabled
Images|many|1|12.39MB|6.224MB (50%)
|1|1|0B|0B
Build Cache|0|0|0B|0B
`
	usage := ParseSystemDF([]byte(noisy))
	if len(usage) != 2 {
		t.Fatalf("read %d rows: %+v", len(usage), usage)
	}
	if usage[0].Kind != "Images" || usage[0].Size != "12.39MB" {
		t.Errorf("a row with an unreadable count was lost: %+v", usage[0])
	}
	if usage[0].Total != 0 {
		t.Errorf("an unreadable count became %d", usage[0].Total)
	}
	if _, ok := Find(usage, "Build Cache"); !ok {
		t.Error("a row after the bad ones was dropped")
	}
}

// Not scoped to a project, and it cannot be: images, volumes and build cache
// are the daemon's, shared between everything on the host — which is what
// makes the answer worth having, since the thing filling the disk is usually
// not this project's.
func TestSystemDFCommandAsksTheDaemon(t *testing.T) {
	command := SystemDFCommand()
	if !strings.Contains(command, "docker system df") {
		t.Errorf("unexpected command: %s", command)
	}
	if !strings.Contains(command, "--format") {
		t.Errorf("the padded table is being parsed instead of a format: %s", command)
	}
	for _, field := range []string{"{{.Type}}", "{{.Size}}", "{{.Reclaimable}}"} {
		if !strings.Contains(command, field) {
			t.Errorf("%s missing from the format: %s", field, command)
		}
	}
}

// The daemon prints a row per kind and no summary line, so the total is
// added up here. It is the number the decision turns on — whether a prune is
// worth running — and a share of a whole is the one thing the screen can
// honestly draw a meter for.
func TestTotals(t *testing.T) {
	held, reclaimable, ok := Totals(ParseSystemDF([]byte(dfOutput)))
	if !ok {
		t.Fatal("nothing was totalled from a full answer")
	}
	// 12.39MB + 32.77kB, of which 6.224MB + 8.192kB can go.
	if got := FormatBytes(held); got != "12.42MB" {
		t.Errorf("held = %q", got)
	}
	if got := FormatBytes(reclaimable); got != "6.232MB" {
		t.Errorf("reclaimable = %q", got)
	}

	// A daemon that answered nothing readable is not a daemon holding
	// nothing, and the difference has to reach the caller.
	if _, _, ok := Totals(nil); ok {
		t.Error("an empty answer was totalled")
	}
	if _, _, ok := Totals([]DiskUsage{{Kind: "Images", Size: "who knows"}}); ok {
		t.Error("an unreadable size was totalled")
	}
}

// The sizes come back out in the same units the daemon writes them in, which
// count in thousands rather than in 1024s.
func TestParseSize(t *testing.T) {
	cases := []struct {
		text  string
		bytes uint64
		ok    bool
	}{
		{"0B", 0, true},
		{"8.192kB", 8192, true},
		{"12.39MB", 12_390_000, true},
		{"48.21GB", 48_210_000_000, true},
		{"1.5TB", 1_500_000_000_000, true},
		// Docker writes kilobytes as `kB`; another release writing `KB`
		// must not be silently dropped, because a dropped row is a total
		// that is quietly too small.
		{"8.192KB", 8192, true},
		{"", 0, false},
		{"lots", 0, false},
		{"12.39", 0, false},
	}
	for _, test := range cases {
		got, ok := parseSize(test.text)
		if ok != test.ok || (ok && got != test.bytes) {
			t.Errorf("parseSize(%q) = %d, %v; want %d, %v",
				test.text, got, ok, test.bytes, test.ok)
		}
	}
}

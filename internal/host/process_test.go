package host

// Tests for the process table. The stat lines are real output, captured from
// the e2e fixture as the unprivileged operator account, plus the two shapes
// a real host produces that a fixture of twenty processes does not: a name
// with a space in it and a name with a parenthesis, both of which break
// anything that counts fields from the left.

import (
	"strings"
	"testing"
)

const processOutput = `#pagesize
4096
#clk
100
#uptime
3600.00 14000.50
#procs
1 (docker-init) S 0 1 1 0 -1 4194560 1860 12222977 0 33577 57 233 3987 11107 20 0 1 0 66803 880640 96 18446744073709551615 1 1 0 0 0 0 0 3145728 0 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
25 (sshd) S 1 24 24 0 -1 4194624 3263 2122825 0 24 2 3 1177 521 20 0 1 0 66822 6311936 780 18446744073709551615 1 1 0 0 0 0 0 4096 81925 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
399 (containerd) S 68 399 399 0 -1 4194560 41938 44049 0 0 10829 4433 27 15 20 0 11 0 68428 1328054272 11450 18446744073709551615 1 1 0 0 0 0 1002055680 0 2143420159 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
`

func TestParseProcessSample(t *testing.T) {
	sample := ParseProcessSample([]byte(processOutput))

	if len(sample.Processes) != 3 {
		t.Fatalf("read %d processes, want 3: %+v", len(sample.Processes), sample.Processes)
	}
	if sample.ClockTck != 100 {
		t.Errorf("ClockTck = %d", sample.ClockTck)
	}
	if sample.UptimeSeconds != 3600 {
		t.Errorf("UptimeSeconds = %v", sample.UptimeSeconds)
	}

	init := sample.Processes[0]
	if init.PID != 1 || init.Name != "docker-init" || init.State != "S" {
		t.Errorf("first process = %+v", init)
	}
	// utime 57 plus stime 233.
	if init.CPUTicks != 290 {
		t.Errorf("CPUTicks = %d, want user and system added", init.CPUTicks)
	}
	if init.Threads != 1 {
		t.Errorf("Threads = %d", init.Threads)
	}
	// 96 pages of 4096 bytes.
	if init.RSSKB != 384 {
		t.Errorf("RSSKB = %d, want the pages converted", init.RSSKB)
	}
	if got := sample.Processes[2].RSSKB; got != 11450*4 {
		t.Errorf("containerd RSSKB = %d", got)
	}
}

// A process name may contain spaces and parentheses. Everything that counts
// space-separated fields from the left gets the wrong column for those, which
// is the reason this is parsed here and not sorted on the server.
func TestAProcessNameMayContainAnything(t *testing.T) {
	awkward := `#procs
742 (Web Content) S 1 742 742 0 -1 4194560 100 0 0 0 40 20 0 0 20 0 8 0 500 1000 250 0 0 0 0 0 0 0 0 0 0 0 0 0 0
` + "808 (foo (bar)) S 1 808 808 0 -1 4194560 100 0 0 0 5 5 0 0 20 0 1 0 500 1000 100 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
	sample := ParseProcessSample([]byte(awkward))
	if len(sample.Processes) != 2 {
		t.Fatalf("read %d processes: %+v", len(sample.Processes), sample.Processes)
	}
	if got := sample.Processes[0]; got.Name != "Web Content" || got.RSSKB != 1000 || got.CPUTicks != 60 {
		t.Errorf("a name with a space parsed as %+v", got)
	}
	if got := sample.Processes[1]; got.Name != "foo (bar)" || got.RSSKB != 400 {
		t.Errorf("a name with parentheses parsed as %+v", got)
	}
}

// A page is not always four kilobytes, so the host is asked.
func TestPageSizeComesFromTheHost(t *testing.T) {
	large := strings.Replace(processOutput, "#pagesize\n4096", "#pagesize\n65536", 1)
	sample := ParseProcessSample([]byte(large))
	if got := sample.Processes[0].RSSKB; got != 96*64 {
		t.Errorf("RSSKB = %d, want the host's own page size applied", got)
	}
	// A host that does not answer still gets a plausible reading rather than
	// zero memory everywhere.
	missing := strings.Replace(processOutput, "#pagesize\n4096", "#pagesize\n", 1)
	if got := ParseProcessSample([]byte(missing)).Processes[0].RSSKB; got != 384 {
		t.Errorf("RSSKB without a page size = %d, want the default applied", got)
	}
	if got := ParseProcessSample([]byte(missing)).ClockTck; got != defaultClockTck {
		t.Errorf("ClockTck without an answer = %d", got)
	}
}

// A truncated or unparseable line is one process missing, not a sample lost.
func TestParseProcessSampleSkipsWhatItCannotRead(t *testing.T) {
	broken := `#procs
1 (docker-init) S 0 1 1
not a stat line at all
25 (sshd) S 1 24 24 0 -1 4194624 3263 2122825 0 24 2 3 1177 521 20 0 1 0 66822 6311936 780 0 0 0 0 0 0 0 0 0 0 0 0 0
`
	sample := ParseProcessSample([]byte(broken))
	if len(sample.Processes) != 1 || sample.Processes[0].PID != 25 {
		t.Errorf("got %+v, want only the whole line", sample.Processes)
	}
}

// A CPU share is the difference between two samples, in top's convention:
// one core fully busy is 100%.
func TestUsageSince(t *testing.T) {
	before := ParseProcessSample([]byte(processOutput))
	// Ten seconds later, containerd has spent a full second of CPU and
	// docker-init half of one.
	after := ParseProcessSample([]byte(strings.NewReplacer(
		"3600.00", "3610.00",
		"12222977 0 33577 57 233", "12222977 0 33577 57 283", // +50 ticks
		"44049 0 0 10829 4433", "44049 0 0 11829 4433", // +1000 ticks
	).Replace(processOutput)))

	usage := after.UsageSince(before)
	byName := map[string]ProcessUsage{}
	for _, entry := range usage {
		byName[entry.Name] = entry
	}
	// 1000 ticks at 100 a second, over ten seconds: one core, all of it.
	if got := byName["containerd"].CPUPercent; got != 100 {
		t.Errorf("containerd = %v%%, want a full core", got)
	}
	if got := byName["docker-init"].CPUPercent; got != 5 {
		t.Errorf("docker-init = %v%%, want 5", got)
	}
	if got := byName["sshd"]; got.CPUPercent != 0 || !got.Measured {
		t.Errorf("an idle process = %+v, want a measured zero", got)
	}
}

// The first sample has nothing to be measured against, and a process that
// has just started was not in the previous one. Both are still listed:
// memory is a single reading, and it is what this view is opened for.
func TestUsageWithNothingToCompareAgainst(t *testing.T) {
	sample := ParseProcessSample([]byte(processOutput))
	usage := sample.UsageSince(ProcessSample{})
	if len(usage) != 3 {
		t.Fatalf("got %d readings, want every process", len(usage))
	}
	for _, entry := range usage {
		if entry.Measured {
			t.Errorf("%s reported a share with nothing to compare against", entry.Name)
		}
		if entry.RSSKB == 0 {
			t.Errorf("%s lost its memory reading", entry.Name)
		}
	}
}

func TestProcessCommandAsksForWhatItNeeds(t *testing.T) {
	command := ProcessCommand()
	for _, want := range []string{
		"/proc/[0-9]*/stat", // every process, in the one format every Linux has
		"getconf PAGESIZE",  // a page is not always four kilobytes
		"getconf CLK_TCK",
		"/proc/uptime", // the clock the share is measured against
	} {
		if !strings.Contains(command, want) {
			t.Errorf("%q missing from the command: %s", want, command)
		}
	}
	// A process that exits between the glob and the read must not put an
	// error in the output.
	if !strings.Contains(command, "2>/dev/null") {
		t.Error("a process that vanished would write to stderr")
	}
	// Nothing is ranked on the server: `sort -k24` counts fields from the
	// left, and field two is a name that may contain spaces.
	if strings.Contains(command, "sort") || strings.Contains(command, "head") {
		t.Errorf("the list is being narrowed on the server: %s", command)
	}
}

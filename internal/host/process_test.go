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
/proc/1/stat:1 (docker-init) S 0 1 1 0 -1 4194560 1860 12222977 0 33577 57 233 3987 11107 20 0 1 0 66803 880640 96 18446744073709551615 1 1 0 0 0 0 0 3145728 0 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
/proc/25/stat:25 (sshd) S 1 24 24 0 -1 4194624 3263 2122825 0 24 2 3 1177 521 20 0 1 0 66822 6311936 780 18446744073709551615 1 1 0 0 0 0 0 4096 81925 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
/proc/399/stat:399 (containerd) S 68 399 399 0 -1 4194560 41938 44049 0 0 10829 4433 27 15 20 0 11 0 68428 1328054272 11450 18446744073709551615 1 1 0 0 0 0 1002055680 0 2143420159 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
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
/proc/742/stat:742 (Web Content) S 1 742 742 0 -1 4194560 100 0 0 0 40 20 0 0 20 0 8 0 500 1000 250 0 0 0 0 0 0 0 0 0 0 0 0 0 0
` + "/proc/808/stat:808 (foo (bar)) S 1 808 808 0 -1 4194560 100 0 0 0 5 5 0 0 20 0 1 0 500 1000 100 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
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
/proc/1/stat:1 (docker-init) S 0 1 1
not a stat line at all
/proc/25/stat:25 (sshd) S 1 24 24 0 -1 4194624 3263 2122825 0 24 2 3 1177 521 20 0 1 0 66822 6311936 780 0 0 0 0 0 0 0 0 0 0 0 0 0
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

// A PID is reused once its process exits. The process now holding 399
// started later than the one that held it, and its counters measured
// against the old one's would be a share of nothing — a newcomer, not a
// measurement.
func TestAReusedPIDIsANewProcess(t *testing.T) {
	before := ParseProcessSample([]byte(processOutput))
	if got := before.Processes[2].StartTicks; got != 68428 {
		t.Fatalf("StartTicks = %d, want field 22", got)
	}
	after := ParseProcessSample([]byte(strings.NewReplacer(
		"3600.00", "3610.00",
		"20 0 11 0 68428", "20 0 11 0 360500", // started after the first sample
		"44049 0 0 10829 4433", "44049 0 0 11829 4433",
	).Replace(processOutput)))

	for _, entry := range after.UsageSince(before) {
		if entry.Name == "containerd" && entry.Measured {
			t.Errorf("a reused PID was measured against its old process: %v%%", entry.CPUPercent)
		}
		if entry.Name == "sshd" && !entry.Measured {
			t.Error("a process that kept its PID and start time lost its share")
		}
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
	// Every line of a stat file must say which file it came from, or a name
	// with a newline in it can pass for a record or a marker.
	if !strings.Contains(command, "grep -H '' /proc/[0-9]*/stat") {
		t.Errorf("the stat lines are not prefixed with their file: %s", command)
	}
}

// A process names itself, newlines included. Read with `cat`, a name of
// "\n#x" or "\n#procs" started a line that passed for a section marker, and
// the process — with every one read after it — vanished from the table.
// Every line grep prints carries its file, so the record is put back
// together and the name drawn with the newline made visible.
func TestAProcessCannotHideBehindItsName(t *testing.T) {
	for _, name := range []string{"\n#x", "\n#procs", "a\n/proc/1/stat:1 (fake"} {
		hostile := "#procs\n" +
			"/proc/7/stat:7 (" + strings.ReplaceAll(name, "\n", "\n/proc/7/stat:") +
			") S 1 7 7 0 -1 4194560 100 0 0 0 40 20 0 0 20 0 1 0 500 1000 250 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n" +
			"/proc/25/stat:25 (sshd) S 1 24 24 0 -1 4194624 3263 2122825 0 24 2 3 1177 521 20 0 1 0 66822 6311936 780 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
		sample := ParseProcessSample([]byte(hostile))
		if len(sample.Processes) != 2 {
			t.Fatalf("name %q: read %d processes, want 2: %+v", name, len(sample.Processes), sample.Processes)
		}
		hidden := sample.Processes[0]
		if hidden.PID != 7 || hidden.CPUTicks != 60 || hidden.RSSKB != 1000 {
			t.Errorf("name %q: the process read as %+v", name, hidden)
		}
		if want := strings.ReplaceAll(name, "\n", "?"); hidden.Name != want {
			t.Errorf("name %q: drawn as %q, want %q", name, hidden.Name, want)
		}
		if sample.Processes[1].Name != "sshd" {
			t.Errorf("name %q: the process after it read as %+v", name, sample.Processes[1])
		}
	}
}

// Only this package's markers open a section; anything else that begins with
// `#` is what a tool printed.
func TestOnlyKnownMarkersOpenASection(t *testing.T) {
	sections := split("#uptime\n3600.00 1.00\n#not-a-marker\n#mounts\nx\n")
	if got := sections[uptimeMarker]; got != "3600.00 1.00\n#not-a-marker\n" {
		t.Errorf("uptime section = %q", got)
	}
	if _, ok := sections["#not-a-marker"]; ok {
		t.Error("an unknown marker opened a section")
	}
}

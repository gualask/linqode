package host

// What the machine is running, read the same way everything else here is:
// out of /proc rather than out of a tool's output.
//
// `ps` looked like the obvious source and is not one. Measured against the
// e2e fixture's busybox: no `--sort`, no `pcpu` column at all (its `-o`
// accepts user, group, comm, args, pid, ppid, pgid, etime, nice, rgroup,
// ruser, time, tty, vsz, sid, stat, rss and nothing else), `ps aux` silently
// ignores the flags and prints four columns of its own that share neither
// order nor content with procps', and `-o rss` prints `93m` rather than a
// number. Parsing that positionally would have produced confident nonsense
// on exactly the hosts this tool is meant to reach.
//
// /proc/<pid>/stat has all of it — the name, the memory, the CPU time, the
// thread count — in one line per process, in one format, on every Linux.
// The cost is that there is one file per process and no way to narrow them
// on the server without putting a sort and a field-counting program there:
// see the comment on ProcessCommand.

import (
	"iter"
	"strconv"
	"strings"
	"unicode"
)

const (
	processMarker  = "#procs"
	pageSizeMarker = "#pagesize"
	clockMarker    = "#clk"
)

// Defaults for the two constants the host is asked for, used when it does
// not answer. Both have been these values on every mainstream Linux for
// twenty years; asking is cheap insurance against the architectures where
// they are not (64K pages on some arm64 kernels).
const (
	defaultPageSize = 4096
	defaultClockTck = 100
)

// ProcessCommand reads every process's stat line, plus the two constants
// needed to turn its numbers into bytes and seconds, plus the clock the
// interval is measured against.
//
// Nothing narrows the list on the server, and that is deliberate. Sorting it
// there means `sort -k24`, which counts space-separated fields — and field 2
// is the process name in parentheses, which may contain spaces. On a host
// running anything with a space in its name, every column after it shifts and
// the sort silently ranks by the wrong number. Ranking is done here instead,
// where the name is found by looking for its closing parenthesis rather than
// by counting.
//
// The stat files are read with `grep -H` rather than `cat`, because the name
// is whatever the process set it to — `prctl(PR_SET_NAME)` takes any byte but
// NUL, newlines included. Concatenated, a name of "\n#procs" would start a
// line of its own and could pass for a marker or a record, hiding the
// process and everything read after it. Prefixed, every line of the output
// starts with the file it came from, which no name can forge, so a record is
// the run of lines carrying the same path. `LC_ALL=C` keeps GNU grep from
// calling a name that is not valid UTF-8 binary and printing a summary in
// place of the line.
//
// What that costs is roughly 235 bytes per process — about 20 KB on a host
// running a hundred of them. It is why this is not on the home: the reading
// is taken only while the system view is open, at which point the operator
// has asked for it.
func ProcessCommand() string {
	return "echo '" + pageSizeMarker + "'; getconf PAGESIZE 2>/dev/null; " +
		"echo '" + clockMarker + "'; getconf CLK_TCK 2>/dev/null; " +
		"echo '" + uptimeMarker + "'; cat /proc/uptime; " +
		"echo '" + processMarker + "'; LC_ALL=C grep -H '' /proc/[0-9]*/stat 2>/dev/null"
}

// Process is one running process.
type Process struct {
	PID int
	// Name is the kernel's `comm`: the executable's name, capped at fifteen
	// characters. The full command line is another file per process, and
	// this is enough to say what is running.
	Name    string
	State   string
	Threads int
	RSSKB   uint64
	// CPUTicks is user plus system time since the process started, in the
	// clock ticks /proc counts in. Like every other counter here it means
	// nothing alone.
	CPUTicks uint64
}

// ProcessSample is one reading of the process table, with the host's clock
// so two of them can be subtracted.
type ProcessSample struct {
	UptimeSeconds float64
	ClockTck      uint64
	Processes     []Process
}

// ParseProcessSample reads the output of ProcessCommand. A process that
// exited between the glob and the read simply is not there.
func ParseProcessSample(raw []byte) ProcessSample {
	sections := split(string(raw))

	sample := ProcessSample{ClockTck: defaultClockTck}
	if ticks, err := strconv.ParseUint(strings.TrimSpace(sections[clockMarker]), 10, 64); err == nil && ticks > 0 {
		sample.ClockTck = ticks
	}
	pageSize := uint64(defaultPageSize)
	if size, err := strconv.ParseUint(strings.TrimSpace(sections[pageSizeMarker]), 10, 64); err == nil && size > 0 {
		pageSize = size
	}
	if fields := strings.Fields(sections[uptimeMarker]); len(fields) >= 1 {
		sample.UptimeSeconds, _ = strconv.ParseFloat(fields[0], 64)
	}

	for record := range processRecords(sections[processMarker]) {
		if process, ok := parseProcess(record, pageSize); ok {
			sample.Processes = append(sample.Processes, process)
		}
	}
	return sample
}

// processRecords puts each stat file back together out of grep's prefixed
// lines: consecutive lines naming the same file are one record, joined by
// the newlines the process name had in it. The path is cut at its first
// colon, which a path under /proc/<pid> does not contain.
func processRecords(section string) iter.Seq[string] {
	return func(yield func(string) bool) {
		current := ""
		var record strings.Builder
		for line := range strings.Lines(section) {
			path, content, found := strings.Cut(strings.TrimRight(line, "\r\n"), ":")
			if !found {
				continue
			}
			if path != current && record.Len() > 0 {
				if !yield(record.String()) {
					return
				}
				record.Reset()
			}
			if path == current {
				record.WriteByte('\n')
			}
			current = path
			record.WriteString(content)
		}
		if record.Len() > 0 {
			yield(record.String())
		}
	}
}

// printableName is a process name fit to be drawn: every control character,
// the newlines that defeated the parser among them, shown as `?` the way
// procps' `ps` shows them.
func printableName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, name)
}

// The offsets of the fields worth reading, counted from the one after the
// process name — which is where counting can start safely, because the name
// is the only field that can contain a space.
const (
	fieldState    = 0
	fieldUTime    = 11
	fieldSTime    = 12
	fieldThreads  = 17
	fieldRSSPages = 21
)

func parseProcess(line string, pageSize uint64) (Process, bool) {
	line = strings.TrimRight(line, "\r\n")
	// The name is wrapped in parentheses and may contain both spaces and
	// parentheses of its own, so it ends at the *last* one on the line.
	open := strings.IndexByte(line, '(')
	closing := strings.LastIndexByte(line, ')')
	if open < 0 || closing < open {
		return Process{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil {
		return Process{}, false
	}
	fields := strings.Fields(line[closing+1:])
	if len(fields) <= fieldRSSPages {
		return Process{}, false
	}

	process := Process{PID: pid, Name: printableName(line[open+1 : closing]), State: fields[fieldState]}
	utime, errUser := strconv.ParseUint(fields[fieldUTime], 10, 64)
	stime, errSystem := strconv.ParseUint(fields[fieldSTime], 10, 64)
	if errUser != nil || errSystem != nil {
		return Process{}, false
	}
	process.CPUTicks = utime + stime
	process.Threads, _ = strconv.Atoi(fields[fieldThreads])
	if pages, err := strconv.ParseUint(fields[fieldRSSPages], 10, 64); err == nil {
		process.RSSKB = pages * pageSize / 1024
	}
	return process, true
}

// ProcessUsage is one process with the share of a CPU it used between two
// samples. The convention is top's: one core fully busy is 100%, so a
// process on four cores reads 400% and the number does not change meaning
// with the size of the machine.
type ProcessUsage struct {
	Process
	CPUPercent float64
	// Measured is false for a process that was not in the previous sample —
	// one that has just started, which has no share to compute yet.
	Measured bool
}

// UsageSince measures this sample against the one before it. Without a
// previous sample every process is still worth listing: memory is a single
// reading, and it is the one an operator opens this view for.
func (s ProcessSample) UsageSince(previous ProcessSample) []ProcessUsage {
	elapsed := s.UptimeSeconds - previous.UptimeSeconds
	before := make(map[int]uint64, len(previous.Processes))
	for _, process := range previous.Processes {
		before[process.PID] = process.CPUTicks
	}

	usage := make([]ProcessUsage, 0, len(s.Processes))
	for _, process := range s.Processes {
		entry := ProcessUsage{Process: process}
		ticks, seen := before[process.PID]
		if seen && elapsed > 0 && process.CPUTicks >= ticks && s.ClockTck > 0 {
			entry.CPUPercent = float64(process.CPUTicks-ticks) /
				float64(s.ClockTck) / elapsed * 100
			entry.Measured = true
		}
		usage = append(usage, entry)
	}
	return usage
}

package system

// Tests for the meter band: what it drops when the terminal narrows, what it
// leaves out when the kernel does not report it, and the thresholds that make
// it scannable. The screen-level questions — whether the band is on the header
// at all, whether it went stale — live with the screen, in internal/tui/home.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/theme"
)

func sampleMetrics() host.Metrics {
	return host.Metrics{
		Load1: 0.50, Load5: 0.40, Load15: 0.30,
		CPUs:           4,
		Uptime:         90 * time.Minute,
		MemTotalKB:     2048000,
		MemAvailableKB: 1024000,
		DiskTotalKB:    20000000,
		DiskUsedKB:     5000000,
	}
}

// sampled is a model holding one good sample, which is the state every one of
// these tests starts from.
func sampled(metrics host.Metrics) *Model {
	m := New("", "")
	m.SetSample(metrics, nil)
	return m
}

// The bars share whatever the labels and amounts leave, so a wider terminal
// buys resolution rather than padding.
func TestMeterBarsStretchWithTheTerminal(t *testing.T) {
	m := sampled(sampleMetrics())

	cells := func(width int) int { return barCells(m, width) }
	narrow, wide := cells(80), cells(160)

	if wide <= narrow {
		t.Errorf("bars did not grow with the terminal: %d cells at 80, %d at 160", narrow, wide)
	}
	if narrow < 3*meterMinBar {
		t.Errorf("bars fell below the floor at 80 columns: %d cells", narrow)
	}
}

// barCells is how many cells the band spends on its gauges at this width.
// A gauge has no glyph of its own where it is empty and no bracket around it
// either, so it is measured between the label that opens a meter and the
// amount that closes it rather than counted.
func barCells(m *Model, width int) int {
	line := ansi.Strip(m.Band(width))
	total := 0
	for _, gauge := range m.meters() {
		start := strings.Index(line, gauge.label+" ")
		if start < 0 {
			// Shed at this width, which is a finding of its own tests.
			continue
		}
		start += len(gauge.label) + 1
		if end := strings.Index(line[start:], " "+gauge.value); end >= 0 {
			total += end
		}
	}
	return total
}

// The band must never be the thing that wraps the header.
func TestMeterBandFitsItsWidth(t *testing.T) {
	m := sampled(sampleMetrics())
	for _, width := range []int{80, 100, 130, 200} {
		if got := lipgloss.Width(m.Band(width)); got > width {
			t.Errorf("band is %d wide at %d columns", got, width)
		}
	}
}

// Before the first sample there is nothing to draw, and the header must not
// reserve a row for numbers that are not there.
//
// The row itself is the header's and is drawn either way — one that appeared
// on the first sample would push the body down a line a second after the
// screen opened — so what must not appear is a *reading*, not the row.
func TestNoNumbersBeforeTheFirstSample(t *testing.T) {
	m := New("", "")
	if m.HasBand() {
		t.Error("a sample was claimed before any arrived")
	}
	line := m.Band(120)
	if !strings.Contains(line, "waiting") {
		t.Errorf("the band says nothing about having no sample yet: %q", line)
	}
	for _, meter := range []string{"cpu ", "mem ", "swap ", "%"} {
		if strings.Contains(line, meter) {
			t.Errorf("the band drew %q with no sample behind it: %q", meter, line)
		}
	}
}

// A kernel that does not report one of the sources still yields a useful
// line from the rest.
func TestHostLineDropsUnreportedParts(t *testing.T) {
	m := sampled(host.Metrics{Load1: 1.5, CPUs: 2})

	line := m.Band(120)
	if !strings.Contains(line, "load ") || !strings.Contains(line, "1.50") {
		t.Errorf("load missing from band: %q", line)
	}
	for _, absent := range []string{"mem", "disk", "up "} {
		if strings.Contains(line, absent) {
			t.Errorf("band reports %q it never received: %q", absent, line)
		}
	}
}

func TestFormatKB(t *testing.T) {
	cases := []struct {
		kb   uint64
		want string
	}{
		{0, "0.0K"},
		{512, "512K"},
		{1024, "1.0M"},
		{2048000, "2.0G"},
		{20000000, "19.1G"},
	}
	for _, c := range cases {
		if got := formatKB(c.kb); got != c.want {
			t.Errorf("formatKB(%d) = %q, want %q", c.kb, got, c.want)
		}
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Minute, "45m"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "1d1h"},
		{72 * time.Hour, "3d0h"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// The thresholds are what make the line scannable; pin them so a refactor
// cannot quietly turn every value green.
func TestUsageAndLoadThresholds(t *testing.T) {
	if theme.Usage(50).GetForeground() != theme.Green.GetForeground() {
		t.Error("50% should render green")
	}
	if theme.Usage(80).GetForeground() != theme.Yellow.GetForeground() {
		t.Error("80% should render yellow")
	}
	if theme.Usage(95).GetForeground() != theme.Red.GetForeground() {
		t.Error("95% should render red")
	}
	if loadStyle(0.5).GetForeground() != theme.Green.GetForeground() {
		t.Error("half-busy load should render green")
	}
	if loadStyle(1.2).GetForeground() != theme.Red.GetForeground() {
		t.Error("load above one per core should render red")
	}
}

// richMetrics is a sample with everything phase C added: two filesystems,
// swap in use, per-core counters and interface counters.
func richMetrics() host.Metrics {
	m := sampleMetrics()
	m.UptimeSeconds = 5400
	m.SwapTotalKB, m.SwapFreeKB = 2048000, 1024000
	m.Filesystems = []host.Filesystem{
		{Device: "/dev/vda1", Mount: "/", TotalKB: 20000000, UsedKB: 5000000},
		{Device: "/dev/vdb1", Mount: "/var", TotalKB: 40000000, UsedKB: 36000000},
	}
	m.CPUTimes = []host.CPUTime{
		{Name: "cpu", Total: 4000, Idle: 3600},
		{Name: "cpu0", Total: 1000, Idle: 900},
		{Name: "cpu1", Total: 1000, Idle: 900},
		{Name: "cpu2", Total: 1000, Idle: 900},
		{Name: "cpu3", Total: 1000, Idle: 900},
	}
	m.Interfaces = []host.Interface{
		{Name: "lo", RxBytes: 1000, TxBytes: 1000},
		{Name: "eth0", RxBytes: 100000, TxBytes: 50000},
	}
	m.Pressure = host.PressureSet{
		CPU:     host.Pressure{Some10: 12.5},
		IO:      host.Pressure{Some10: 30.0, Full10: 25.0},
		Memory:  host.Pressure{},
		Present: true,
	}
	return m
}

// measured is a model that has seen two samples, which is what it takes for
// a percentage or a rate to exist at all. Ten seconds of host time pass
// between them, and in them one core does all the work.
func measured() *Model {
	before := richMetrics()
	m := New("", "")
	m.SetSample(before, nil)

	after := richMetrics()
	after.UptimeSeconds = before.UptimeSeconds + 10
	// A thousand jiffies per core: cpu0 spends all of them busy and the
	// other three spend all of them idle.
	after.CPUTimes = []host.CPUTime{
		{Name: "cpu", Total: 8000, Idle: 6600},
		{Name: "cpu0", Total: 2000, Idle: 900},
		{Name: "cpu1", Total: 2000, Idle: 1900},
		{Name: "cpu2", Total: 2000, Idle: 1900},
		{Name: "cpu3", Total: 2000, Idle: 1900},
	}
	after.Interfaces = []host.Interface{
		{Name: "lo", RxBytes: 900000, TxBytes: 900000},
		{Name: "eth0", RxBytes: 100000 + 20480, TxBytes: 50000 + 10240},
	}
	m.SetSample(after, nil)
	return m
}

// The band's headline reading is the load average only until there is a
// second sample to subtract the first from.
func TestBandShowsLoadUntilItCanShowCPU(t *testing.T) {
	first := sampled(richMetrics())
	if line := first.Band(160); !strings.Contains(line, "load ") || strings.Contains(line, "cpu ") {
		t.Errorf("first sample should fall back to load: %q", line)
	}
	line := measured().Band(160)
	if !strings.Contains(line, "cpu ") || strings.Contains(line, "load ") {
		t.Errorf("second sample should carry the real reading: %q", line)
	}
	// One core of four busy for ten seconds: 25% of the machine.
	if !strings.Contains(line, "25%") {
		t.Errorf("CPU percentage missing from %q", line)
	}
}

// A comfortable root says nothing about the volume that is about to stop
// the deployment, so the one disk meter follows the fullest filesystem and
// says which one it is.
func TestBandDiskMeterFollowsTheFullestFilesystem(t *testing.T) {
	line := sampled(richMetrics()).Band(160)
	if !strings.Contains(line, "/var ") {
		t.Errorf("band did not follow the fullest filesystem: %q", line)
	}
	if strings.Contains(line, "disk ") {
		t.Errorf("band still labels its disk meter generically: %q", line)
	}
}

// Swap costs the other meters width, so it only appears once it means
// something. Every healthy Linux machine has a little swapped out.
func TestSwapMeterAppearsOnlyWhenSwapIsFilling(t *testing.T) {
	quiet := richMetrics()
	quiet.SwapFreeKB = quiet.SwapTotalKB - 1000
	if line := sampled(quiet).Band(200); strings.Contains(line, "swap ") {
		t.Errorf("a megabyte of swap drew a meter: %q", line)
	}
	if line := sampled(richMetrics()).Band(200); !strings.Contains(line, "swap ") {
		t.Errorf("half the swap in use drew no meter: %q", line)
	}
}

// Everything the batch reads has a row, and the ones that need two samples
// The cores stand in the gauges' track, so the row is as wide as the bars
// above it whatever the machine has: with room to spare a core takes several
// cells, and past that a cell carries the busiest of the ones it covers.
func TestCoreCellsFillTheGaugeWhateverTheCoreCount(t *testing.T) {
	cells := coreCells([]float64{100, 0, 0, 0}, 15)
	if len(cells) != 15 {
		t.Fatalf("four cores over fifteen cells drew %d: %v", len(cells), cells)
	}
	// Three cells a core and one of track between each two: twelve for the
	// cores and three between them, the pinned one first.
	if cells[0] != 100 || cells[2] != 100 || cells[3] != coreGap || cells[4] != 0 || cells[14] != 0 {
		t.Errorf("a core did not take its share of the cells: %v", cells)
	}
	gaps := 0
	for _, cell := range cells {
		if cell == coreGap {
			gaps++
		}
	}
	if gaps != 3 {
		t.Errorf("four cores drew %d gaps, want 3: %v", gaps, cells)
	}

	// Where a gap each does not fit, there are none: seven cores over ten
	// cells would be thirteen.
	for _, cell := range coreCells(make([]float64, 7), 10) {
		if cell == coreGap {
			t.Errorf("seven cores over ten cells drew a gap")
			break
		}
	}

	// Sixty-four cores over ten cells: the pinned one is what the row is
	// read for, so it must survive the crowding.
	cores := make([]float64, 64)
	cores[37] = 100
	cells = coreCells(cores, 10)
	if len(cells) != 10 {
		t.Fatalf("sixty-four cores over ten cells drew %d: %v", len(cells), cells)
	}
	busiest := 0.0
	for _, cell := range cells {
		busiest = max(busiest, cell)
	}
	if busiest != 100 {
		t.Errorf("the pinned core was dropped: %v", cells)
	}

	if got := coreCells(nil, 10); got != nil {
		t.Errorf("no cores drew %v", got)
	}
	if got := coreCells([]float64{50}, 0); got != nil {
		t.Errorf("no room drew %v", got)
	}
}

// wait for the second rather than showing a zero.
func TestSystemViewShowsEveryReading(t *testing.T) {
	m := measured()
	m.SetSize(140, 24)
	view := m.View()

	for _, want := range []string{
		"cpu", "25% busy", "load 0.50", // the machine, and the averages behind it
		"cores", "busiest cpu0 at 100%", // the row that makes the average readable
		"memory", "swap",
		"/var", "/dev/vdb1", // one row per filesystem, named by its device
		"net", "2.0K/s down", "1.0K/s up", "busiest eth0",
		"pressure", "12.5%", "(full 25.0%)",
		"uptime",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("system view missing %q:\n%s", want, view)
		}
	}
}

// A kernel without PSI, a machine without swap, a host whose full df did not
// answer: each leaves its row out rather than drawing an empty one.
func TestSystemViewLeavesOutWhatWasNotReported(t *testing.T) {
	metrics := sampleMetrics() // no swap, no pressure, no mount list
	m := sampled(metrics)
	m.SetSize(140, 24)
	view := m.View()

	for _, absent := range []string{"swap", "pressure", "net "} {
		if strings.Contains(view, absent) {
			t.Errorf("system view reports %q it never received:\n%s", absent, view)
		}
	}
	// The root reading stands in for the mount list, so there is still a
	// filesystem row.
	if !strings.Contains(view, "4.8G used") {
		t.Errorf("root filesystem missing from the view:\n%s", view)
	}
}

// A mount point longer than the label column widens it for every row, so
// the gauges stay in one line.
func TestLongMountPointsKeepTheGaugesAligned(t *testing.T) {
	metrics := richMetrics()
	metrics.Filesystems = append(metrics.Filesystems,
		host.Filesystem{Device: "/dev/vdc1", Mount: "/var/lib/postgresql",
			TotalKB: 1000, UsedKB: 500})
	m := sampled(metrics)
	m.SetSize(160, 24)

	// The readings are the rows before the first blank one: the view draws
	// them first, then the charts and the processes. They used to be picked
	// out by the `[` that opened every gauge, which is gone with the
	// brackets; what is asserted is what always was, that every row pads its
	// label to the same column and so starts its gauge in the same place.
	column := m.labelColumn()
	rows := 0
	for _, line := range strings.Split(m.View(), "\n") {
		line = ansi.Strip(line)
		if strings.TrimSpace(line) == "" {
			break
		}
		label := strings.Fields(line)[0]
		if want := " " + pad(label, column) + " "; !strings.HasPrefix(line, want) {
			t.Errorf("row %q does not reach the gauge column at %d", line, column+2)
		}
		rows++
	}
	if rows < 3 {
		t.Fatalf("expected a reading on every row, got %d", rows)
	}
}

func TestFormatRate(t *testing.T) {
	cases := []struct {
		rate float64
		want string
	}{
		{0, "0B/s"},
		{802, "802B/s"},
		{2048, "2.0K/s"},
		{1500000, "1.4M/s"},
	}
	for _, c := range cases {
		if got := formatRate(c.rate); got != c.want {
			t.Errorf("formatRate(%v) = %q, want %q", c.rate, got, c.want)
		}
	}
}

// errNotNow stands in for a read that failed, which every panel here has to
// survive without blanking what it had.
var errNotNow = errors.New("connection lost")

// key builds a keypress the way the screen delivers one.
func key(text string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}
}

// warmMetrics adds the temperatures of a machine with real sensors: a CPU
// well inside its limit and an NVMe that is not.
func warmMetrics() host.Metrics {
	m := richMetrics()
	m.Sensors = []host.Sensor{
		{Chip: "nvme", Label: "Composite", MilliC: 71_000, LimitMilliC: 84_850},
		{Chip: "coretemp", Label: "Package id 0", MilliC: 58_000, LimitMilliC: 100_000},
		{Chip: "acpitz", MilliC: 41_000},
	}
	return m
}

// The band gets a number rather than a fifth gauge: there is no width for
// one, and the colour carries the judgement a bare temperature cannot make
// for itself.
func TestTheBandCarriesATemperature(t *testing.T) {
	line := sampled(warmMetrics()).Band(160)
	if !strings.Contains(line, "71°C") {
		t.Errorf("the temperature is missing from the band: %q", line)
	}
	if strings.Contains(line, "temp[") {
		t.Errorf("the band drew a fifth gauge: %q", line)
	}
	// It is the hottest by share of its own limit, not the largest number.
	if strings.Contains(line, "58°C") {
		t.Errorf("the band shows more than one sensor: %q", line)
	}
	// A host that reports none says nothing rather than zero degrees.
	if line := sampled(richMetrics()).Band(160); strings.Contains(line, "°C") {
		t.Errorf("a host with no sensors reported a temperature: %q", line)
	}
}

// Uptime goes first when the line runs short, then the temperature, then the
// meters from the bottom — and the band never wraps the header.
func TestTheBandShedsUptimeBeforeTheTemperature(t *testing.T) {
	m := sampled(warmMetrics())
	for _, width := range []int{60, 70, 80, 100, 130, 200} {
		if got := lipgloss.Width(m.Band(width)); got > width {
			t.Errorf("band is %d wide at %d columns: %q", got, width, m.Band(width))
		}
	}
	wide := m.Band(200)
	if !strings.Contains(wide, "71°C") || !strings.Contains(wide, "up ") {
		t.Fatalf("a wide band is missing part of its tail: %q", wide)
	}
	// Somewhere on the way down uptime goes and the temperature stays.
	found := false
	for width := 200; width >= 60; width-- {
		line := m.Band(width)
		if !strings.Contains(line, "up ") && strings.Contains(line, "71°C") {
			found = true
			break
		}
	}
	if !found {
		t.Error("the temperature was never kept in preference to uptime")
	}
}

// The view behind the band draws the meter the band has no room for, against
// the sensor's own limit — and says when that limit is one this tool made up.
func TestTheSystemViewDrawsTheTemperature(t *testing.T) {
	m := sampled(warmMetrics())
	m.SetSize(150, 24)
	view := m.View()

	for _, want := range []string{" temp     ", "71°C Composite", "of 85°C",
		"58°C Package id 0", "41°C acpitz"} {
		if !strings.Contains(view, want) {
			t.Errorf("the temperature row is missing %q:\n%s", want, view)
		}
	}

	metrics := warmMetrics()
	metrics.Sensors = []host.Sensor{{Chip: "cpu_thermal", MilliC: 47_500}}
	bare := sampled(metrics)
	bare.SetSize(150, 24)
	if !strings.Contains(bare.View(), "no limit reported") {
		t.Errorf("a made-up denominator was not declared:\n%s", bare.View())
	}
}

// A path that does not fit its column keeps its tail: the head of a mount
// point is what two rows have in common, and the tail is what tells them
// apart.
func TestLongMountPointsKeepTheEndThatIdentifiesThem(t *testing.T) {
	for _, tc := range []struct {
		label  string
		column int
		want   string
	}{
		{"/", 16, "/               "},
		{"/System/Volumes/Data", 16, "…/Volumes/Data  "},
		{"/var/lib/postgresql", 16, "…/lib/postgresql"},
		// No segment boundary fits, so the cut lands inside the last one
		// rather than giving up and overflowing the column.
		{"/verylongsinglesegment", 12, "…nglesegment"},
		// Not a path: a meter label is one short word and is left alone.
		{"memory", 16, "memory          "},
	} {
		if got := pad(tc.label, tc.column); got != tc.want {
			t.Errorf("pad(%q, %d) = %q, want %q", tc.label, tc.column, got, tc.want)
		}
	}
}

// A mount point in a script whose characters take two cells each is cut by
// cells, not by characters: counting one against the other walked the cut off
// the front of the label and panicked.
func TestWideMountPointsAreCutByCells(t *testing.T) {
	for _, column := range []int{4, 5, 8, 12, 16} {
		got := pad("/データボリュームディスク", column)
		if width := lipgloss.Width(got); width != column {
			t.Errorf("pad(wide, %d) is %d cells: %q", column, width, got)
		}
		if !strings.HasPrefix(got, "…") || !strings.Contains(got, "ク") {
			t.Errorf("pad(wide, %d) = %q, want the tail behind an ellipsis", column, got)
		}
	}
}

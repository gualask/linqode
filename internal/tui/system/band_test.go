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
	m := New()
	m.SetSample(metrics, nil)
	return m
}

// The bars share whatever the labels and amounts leave, so a wider terminal
// buys resolution rather than padding.
func TestMeterBarsStretchWithTheTerminal(t *testing.T) {
	m := sampled(sampleMetrics())

	cells := func(width int) int {
		return strings.Count(m.Band(width), "░") + strings.Count(m.Band(width), "█")
	}
	narrow, wide := cells(80), cells(160)

	if wide <= narrow {
		t.Errorf("bars did not grow with the terminal: %d cells at 80, %d at 160", narrow, wide)
	}
	if narrow < 3*meterMinBar {
		t.Errorf("bars fell below the floor at 80 columns: %d cells", narrow)
	}
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
func TestNoBandBeforeTheFirstSample(t *testing.T) {
	m := New()
	if m.HasBand() {
		t.Error("band claimed a row before any sample arrived")
	}
	if line := m.Band(120); line != "" {
		t.Errorf("band rendered without a sample: %q", line)
	}
}

// A kernel that does not report one of the sources still yields a useful
// line from the rest.
func TestHostLineDropsUnreportedParts(t *testing.T) {
	m := sampled(host.Metrics{Load1: 1.5, CPUs: 2})

	line := m.Band(120)
	if !strings.Contains(line, "load[") || !strings.Contains(line, "1.50") {
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

func TestBarClamps(t *testing.T) {
	if got := bar(0, 4); got != "░░░░" {
		t.Errorf("bar(0) = %q", got)
	}
	if got := bar(100, 4); got != "████" {
		t.Errorf("bar(100) = %q", got)
	}
	// Load can exceed one per core; the bar fills rather than overflowing.
	if got := bar(250, 4); got != "████" {
		t.Errorf("bar(250) = %q", got)
	}
	if got := bar(50, 4); got != "██░░" {
		t.Errorf("bar(50) = %q", got)
	}
	if got := bar(50, 0); got != "" {
		t.Errorf("bar with no width = %q", got)
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
	m := New()
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
	if line := first.Band(160); !strings.Contains(line, "load[") || strings.Contains(line, "cpu[") {
		t.Errorf("first sample should fall back to load: %q", line)
	}
	line := measured().Band(160)
	if !strings.Contains(line, "cpu[") || strings.Contains(line, "load[") {
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
	if !strings.Contains(line, "/var[") {
		t.Errorf("band did not follow the fullest filesystem: %q", line)
	}
	if strings.Contains(line, "disk[") {
		t.Errorf("band still labels its disk meter generically: %q", line)
	}
}

// Swap costs the other meters width, so it only appears once it means
// something. Every healthy Linux machine has a little swapped out.
func TestSwapMeterAppearsOnlyWhenSwapIsFilling(t *testing.T) {
	quiet := richMetrics()
	quiet.SwapFreeKB = quiet.SwapTotalKB - 1000
	if line := sampled(quiet).Band(200); strings.Contains(line, "swap[") {
		t.Errorf("a megabyte of swap drew a meter: %q", line)
	}
	if line := sampled(richMetrics()).Band(200); !strings.Contains(line, "swap[") {
		t.Errorf("half the swap in use drew no meter: %q", line)
	}
}

// Everything the batch reads has a row, and the ones that need two samples
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

	var columns []int
	for _, line := range strings.Split(m.View(), "\n") {
		if index := strings.Index(line, "["); index >= 0 {
			columns = append(columns, lipgloss.Width(line[:index]))
		}
	}
	if len(columns) < 3 {
		t.Fatalf("expected a gauge on every reading, got %d", len(columns))
	}
	for _, column := range columns {
		if column != columns[0] {
			t.Errorf("gauges start at different columns: %v", columns)
			break
		}
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

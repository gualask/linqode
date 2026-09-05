package system

// Tests for the process list: the ranking, the two forms the panel takes,
// and the space it is allowed to use.

import (
	"strings"
	"testing"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
)

// running is a machine with a memory hog that is idle and a busy process
// that is small — the pair that makes the two rankings disagree, which is
// the only reason there are two.
func running() host.ProcessSample {
	return host.ProcessSample{
		UptimeSeconds: 1000, ClockTck: 100,
		Processes: []host.Process{
			{PID: 101, Name: "postgres", RSSKB: 1_100_000, CPUTicks: 5_000},
			{PID: 202, Name: "ffmpeg", RSSKB: 40_000, CPUTicks: 900_000},
			{PID: 303, Name: "node", RSSKB: 410_000, CPUTicks: 20_000},
			{PID: 404, Name: "sshd", RSSKB: 3_000, CPUTicks: 10},
		},
	}
}

// measuredProcesses is a panel that has read the table twice, ten seconds
// apart, which is what it takes for a share to exist.
func measuredProcesses() *Model {
	m := sampled(richMetrics())
	m.SetOpen(true)
	m.SetSize(120, 24)

	before := running()
	m.SetProcesses(before, nil)

	after := running()
	after.UptimeSeconds += 10
	// ffmpeg burns two full cores; postgres does almost nothing.
	after.Processes[1].CPUTicks += 2_000
	after.Processes[0].CPUTicks += 10
	m.SetProcesses(after, nil)
	return m
}

func TestTheProcessListRanksByMemoryFirst(t *testing.T) {
	view := measuredProcesses().View()
	block := view[strings.Index(view, "processes"):]
	if !strings.Contains(block, "by memory") {
		t.Errorf("the list does not say what it is ranked by:\n%s", block)
	}
	postgres := strings.Index(block, "postgres")
	ffmpeg := strings.Index(block, "ffmpeg")
	if postgres < 0 || ffmpeg < 0 {
		t.Fatalf("processes missing from the list:\n%s", block)
	}
	if postgres > ffmpeg {
		t.Errorf("the memory ranking put the small process first:\n%s", block)
	}
	// Two thousand ticks over ten seconds at a hundred a second: two cores.
	if !strings.Contains(block, "200.0%") {
		t.Errorf("the CPU share is missing or wrong:\n%s", block)
	}
	if !strings.Contains(block, "1.0G") {
		t.Errorf("the memory reading is missing:\n%s", block)
	}
}

// `s` switches which question the list answers. Neither order answers the
// other's: memory is who is holding the machine's RAM, CPU is who is burning
// it right now.
func TestSortingByCPUPutsTheBusyProcessFirst(t *testing.T) {
	m := measuredProcesses()
	m.toggleRanking()

	view := m.View()
	block := view[strings.Index(view, "processes"):]
	if !strings.Contains(block, "by cpu") {
		t.Errorf("the list did not say it changed order:\n%s", block)
	}
	if strings.Index(block, "ffmpeg") > strings.Index(block, "postgres") {
		t.Errorf("the cpu ranking put the idle process first:\n%s", block)
	}
	m.toggleRanking()
	if m.ranking != byMemory {
		t.Error("the key does not toggle back")
	}
}

// The panel is a header on the home and a view when it is opened, and the
// keys it answers to are not the same in the two forms.
func TestTheHintsSayWhichFormThePanelIsIn(t *testing.T) {
	m := measuredProcesses()
	hints := ""
	for _, hint := range m.Hints() {
		hints += hint.Text + " "
	}
	if !strings.Contains(hints, "s by cpu") {
		t.Errorf("the open view does not offer the ranking: %q", hints)
	}

	m.SetOpen(false)
	hints = ""
	for _, hint := range m.Hints() {
		hints += hint.Text + " "
	}
	if !strings.Contains(hints, "enter system") {
		t.Errorf("the band does not offer the way in: %q", hints)
	}
	if strings.Contains(hints, "s by") {
		t.Errorf("the band offers a key that only the view answers to: %q", hints)
	}
}

// `s` is the view's key, not the band's: on the home it belongs to whatever
// the screen decides, and a header must not quietly swallow it.
func TestTheBandDoesNotAnswerToTheViewsKey(t *testing.T) {
	m := measuredProcesses()
	m.SetOpen(false)
	m.Update(key("s"))
	if m.ranking != byMemory {
		t.Error("the band reordered a list it is not showing")
	}
}

// A short view spends what it has on the readings and shows no list at all:
// half a list is worse than none, because the thing it is read for is the
// top of it.
func TestTheListTakesWhatIsLeftAndNoMore(t *testing.T) {
	m := measuredProcesses()

	m.SetSize(120, 40)
	if got := strings.Count(m.View(), "\n"); got == 0 {
		t.Fatal("nothing rendered")
	}
	tall := strings.Count(m.View(), "sshd")
	m.SetSize(120, 11)
	short := strings.Count(m.View(), "sshd")
	if tall != 1 {
		t.Errorf("a tall view showed the smallest process %d times", tall)
	}
	if short != 0 {
		t.Errorf("a short view still found room for the last process:\n%s", m.View())
	}

	// And a view with no room for the headings and a couple of rows shows
	// none.
	m.SetSize(120, 9)
	if strings.Contains(m.View(), "processes") {
		t.Errorf("a list was drawn with no room for one:\n%s", m.View())
	}
}

// The block has to fit in the rows it was given. The box truncates from the
// bottom without saying so, and a list whose last row is silently cut is a
// list that lies about what it holds.
func TestTheListFitsTheRowsItWasGiven(t *testing.T) {
	m := measuredProcesses()
	for height := 10; height <= 30; height++ {
		m.SetSize(120, height)
		if got := strings.Count(m.View(), "\n") + 1; got > height {
			t.Errorf("at %d rows the view drew %d lines:\n%s", height, got, m.View())
		}
	}
}

// Nothing has been read until the view is opened, and an empty list is not
// drawn as an empty heading.
func TestNoListBeforeTheFirstReading(t *testing.T) {
	m := sampled(richMetrics())
	m.SetOpen(true)
	m.SetSize(120, 30)
	if strings.Contains(m.View(), "processes") {
		t.Errorf("a process list was drawn before anything was read:\n%s", m.View())
	}
}

// A failed reading keeps the last list on screen and says so, the way every
// other reading here does.
func TestAFailedProcessReadingKeepsTheLastOne(t *testing.T) {
	m := measuredProcesses()
	m.SetProcesses(host.ProcessSample{}, errNotNow)
	view := m.View()
	if !strings.Contains(view, "postgres") {
		t.Errorf("the last good list was dropped:\n%s", view)
	}
	if !strings.Contains(view, "failed") {
		t.Errorf("a stale list is not flagged:\n%s", view)
	}
}

// The daemon's accounting sits under the filesystem rows because it is the
// answer to the question they raise: a /var at 93% says nothing about how
// much of it is images nobody is running.
func TestDockerUsageSitsUnderTheFilesystems(t *testing.T) {
	m := measuredProcesses()
	m.SetDiskUsage(compose.ParseSystemDF([]byte(
		"Images|12|3|48.2GB|31.4GB (65%)\n"+
			"Containers|9|4|1.2GB|400MB (33%)\n"+
			"Local Volumes|4|4|8.9GB|0B\n"+
			"Build Cache|31|0|6.1GB|6.1GB (100%)\n")), nil)

	view := m.View()
	docker := strings.Index(view, " docker ")
	if docker < 0 {
		t.Fatalf("no docker row:\n%s", view)
	}
	if last := strings.LastIndex(view[:docker], "/var"); last < 0 {
		t.Errorf("the docker row is not under the filesystems:\n%s", view)
	}
	if net := strings.Index(view, " net "); net > 0 && net < docker {
		t.Errorf("the docker row landed after the network row:\n%s", view)
	}

	// The headline is the share the daemon would give back, because that is
	// what decides whether a prune is worth running; the breakdown by kind
	// is the dim tail behind it.
	for _, want := range []string{"of 64.4GB reclaimable", "images 48.2GB", "cache 6.1GB"} {
		if !strings.Contains(view, want) {
			t.Errorf("the docker row is missing %q:\n%s", want, view)
		}
	}
	// The idle count belongs to the kinds with something to free; a volume
	// store entirely in use must not be offered for cleanup.
	if !strings.Contains(view, "images 48.2GB (9 idle)") {
		t.Errorf("the idle count is missing:\n%s", view)
	}
	if strings.Contains(view, "volumes 8.9GB (") {
		t.Errorf("a fully used volume store was offered for cleanup:\n%s", view)
	}
	// And it wears the same grammar as every other row: a meter, because a
	// share of something finite is the one thing a meter can honestly draw.
	if !strings.Contains(view, " docker   [") {
		t.Errorf("the docker row has no meter:\n%s", view)
	}
}

// Nothing is drawn before the daemon has answered, and a failed answer keeps
// the last one: it changes slowly enough that a minute-old reading is still
// worth reading.
func TestDockerUsageBeforeAndAfterAFailure(t *testing.T) {
	m := measuredProcesses()
	if strings.Contains(m.View(), " docker ") {
		t.Errorf("a docker row was drawn before the daemon answered:\n%s", m.View())
	}

	m.SetDiskUsage(compose.ParseSystemDF([]byte("Images|1|1|2.0GB|0B\n")), nil)
	m.SetDiskUsage(nil, errNotNow)
	if !strings.Contains(m.View(), "images 2.0GB") {
		t.Errorf("a failed reading dropped the last good one:\n%s", m.View())
	}
}

// A card gets the same grammar as everything else: a meter for what it is
// doing, and the numbers the meter cannot carry beside it.
func TestGPURows(t *testing.T) {
	m := measuredProcesses()
	m.SetGPUs(host.ParseGPUs([]byte(`#amdgpu
/sys/class/drm/card0/device/gpu_busy_percent:62
/sys/class/drm/card0/device/mem_info_vram_used:5368709120
/sys/class/drm/card0/device/mem_info_vram_total:17179869184
/sys/class/drm/card0/device/hwmon/hwmon4/temp1_input:68000
/sys/class/drm/card0/device/hwmon/hwmon4/power1_average:184000000
#nvidia
0, NVIDIA A10, 34, 4123, 23028, 61, 120.45
`)), nil)

	view := m.View()
	// Two cards, so each row says which one it is.
	for _, want := range []string{"gpu0", "gpu1", "62% busy", "34% busy",
		"5.0G/16.0G", "68°C", "184W", "amdgpu card0", "NVIDIA A10"} {
		if !strings.Contains(view, want) {
			t.Errorf("the gpu rows are missing %q:\n%s", want, view)
		}
	}

	// One card needs no number in its label.
	single := measuredProcesses()
	single.SetGPUs(host.ParseGPUs([]byte("#nvidia\n0, NVIDIA A10, 34, 4123, 23028, 61, 120.45\n")), nil)
	got := single.View()
	if strings.Contains(got, "gpu0") {
		t.Errorf("a single card was numbered:\n%s", got)
	}
	if !strings.Contains(got, "gpu ") {
		t.Errorf("the single card lost its row:\n%s", got)
	}
}

// The common case, and the one the fixture proves: no card, no row.
func TestNoGPURowWithoutACard(t *testing.T) {
	m := measuredProcesses()
	if strings.Contains(m.View(), "gpu") {
		t.Errorf("a gpu row was drawn on a host with no card:\n%s", m.View())
	}
	m.SetGPUs(nil, errNotNow)
	if strings.Contains(m.View(), "gpu") {
		t.Errorf("a failed reading drew a row:\n%s", m.View())
	}
}

// An older amdgpu kernel reports memory and no utilisation. The row still
// draws, against the number it does have, and says which one that is.
func TestACardThatReportsNoUtilisation(t *testing.T) {
	m := measuredProcesses()
	m.SetGPUs(host.ParseGPUs([]byte(`#amdgpu
/sys/class/drm/card0/device/mem_info_vram_used:8589934592
/sys/class/drm/card0/device/mem_info_vram_total:17179869184
`)), nil)
	view := m.View()
	if !strings.Contains(view, "50% of memory") {
		t.Errorf("the row does not say which number it is drawing:\n%s", view)
	}
	if strings.Contains(view, "busy") {
		t.Errorf("a utilisation nobody reported was drawn:\n%s", view)
	}
}

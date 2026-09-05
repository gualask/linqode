package home

// Renders the home screen in color to an HTML page, for looking at.
//
// Tests run without a TTY, where lipgloss drops every color under test (see
// docs/tests.md). That leaves a class of defect no assertion catches
// because nobody can see it: a gauge whose empty track took the same
// saturated color as its fill, a heading band shaded so close to the
// selected row that the two read as one thing. Both shipped and both were
// obvious the moment a frame was rendered in color.
//
// This asserts nothing and nothing depends on its output — it is a viewer,
// and the assertions stay in the tests beside it. It lives in a _test.go
// file rather than a package of its own precisely because it is not
// production code: the compiler leaves it out of every ordinary build.
//
//	LINQODE_UI_SHOT=/tmp/shot.html go test ./internal/tui/home/ -run TestUIShot
//
// Without the variable the test skips, so a normal run costs nothing.

import (
	"errors"
	"fmt"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/tui/status"
)

// shotFrame is one captured screen, with a caption saying what state it holds.
type shotFrame struct {
	Name string
	Text string
}

// xterm's first sixteen, as a mainstream dark terminal renders them. The
// point is to be representative, not to match any one emulator exactly.
var base16 = [16]string{
	"#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
	"#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#f5f5f5",
}

const (
	defaultFG = "#d4d4d4"
	defaultBG = "#1e1e1e"
)

// xterm256 resolves a 256-color index: the sixteen base colors, then the
// 6×6×6 cube, then the greyscale ramp.
func xterm256(n int) string {
	switch {
	case n < 16:
		return base16[n]
	case n < 232:
		n -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", level(n/36), level((n%36)/6), level(n%6))
	default:
		v := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
}

// pen is the terminal's current graphic state.
type pen struct {
	fg, bg      string
	bold, faint bool
	reverse     bool
}

func (p *pen) apply(params []int) {
	for i := 0; i < len(params); i++ {
		switch code := params[i]; {
		case code == 0:
			*p = pen{}
		case code == 1:
			p.bold = true
		case code == 2:
			p.faint = true
		case code == 7:
			p.reverse = true
		case code == 22:
			p.bold, p.faint = false, false
		case code == 27:
			p.reverse = false
		case code == 39:
			p.fg = ""
		case code == 49:
			p.bg = ""
		case code >= 30 && code <= 37:
			p.fg = base16[code-30]
		case code >= 40 && code <= 47:
			p.bg = base16[code-40]
		case code >= 90 && code <= 97:
			p.fg = base16[code-90+8]
		case code >= 100 && code <= 107:
			p.bg = base16[code-100+8]
		case (code == 38 || code == 48) && i+2 < len(params) && params[i+1] == 5:
			if code == 38 {
				p.fg = xterm256(params[i+2])
			} else {
				p.bg = xterm256(params[i+2])
			}
			i += 2
		case (code == 38 || code == 48) && i+4 < len(params) && params[i+1] == 2:
			rgb := fmt.Sprintf("#%02x%02x%02x", params[i+2], params[i+3], params[i+4])
			if code == 38 {
				p.fg = rgb
			} else {
				p.bg = rgb
			}
			i += 4
		}
	}
}

func (p *pen) css() string {
	fg, bg := p.fg, p.bg
	if fg == "" {
		fg = defaultFG
	}
	if bg == "" {
		bg = defaultBG
	}
	if p.reverse {
		fg, bg = bg, fg
	}
	style := fmt.Sprintf("color:%s;background:%s", fg, bg)
	if p.bold {
		style += ";font-weight:700"
	}
	if p.faint {
		style += ";opacity:.55"
	}
	return style
}

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// shotRender turns one frame's escape sequences into spans.
func shotRender(frame string) string {
	var b strings.Builder
	var p pen
	last := 0
	emit := func(text string) {
		if text != "" {
			fmt.Fprintf(&b, `<span style="%s">%s</span>`, p.css(), html.EscapeString(text))
		}
	}
	for _, match := range sgr.FindAllStringSubmatchIndex(frame, -1) {
		emit(frame[last:match[0]])
		params := []int{0}
		if raw := frame[match[2]:match[3]]; raw != "" {
			params = params[:0]
			for _, field := range strings.Split(raw, ";") {
				value, _ := strconv.Atoi(field)
				params = append(params, value)
			}
		}
		p.apply(params)
		last = match[1]
	}
	emit(frame[last:])
	return b.String()
}

// shotPage is a standalone HTML document holding every frame, captioned.
func shotPage(title string, frames []shotFrame) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><meta charset="utf-8"><title>%s</title><style>
body{margin:0;padding:24px;background:#111;color:#888;
     font:13px/1.5 -apple-system,system-ui,sans-serif}
h2{font:600 13px/1 inherit;margin:28px 0 8px;color:#aaa}
h2:first-of-type{margin-top:0}
pre{margin:0;padding:14px;background:%s;color:%s;display:inline-block;
    white-space:pre;font:14px/1.35 "SF Mono",Menlo,Consolas,monospace;
    border-radius:6px}
</style>`, html.EscapeString(title), defaultBG, defaultFG)
	for _, frame := range frames {
		fmt.Fprintf(&b, "<h2>%s</h2><pre>%s</pre>\n",
			html.EscapeString(frame.Name), shotRender(frame.Text))
	}
	return b.String()
}

// busyHost is one host sample. round advances it: /proc/stat and
// /proc/net/dev are counters, so the frames only show a CPU percentage or a
// throughput once two rounds have gone by — the same wait a real session
// has, and the reason the band draws load until then.
//
// The shape is a machine worth looking at: one core pinned while the average
// says forty percent, a /var about to fill, and swap in use.
func busyHost(round int) host.Metrics {
	const secondsPerRound = 5
	// Per core, per round: five seconds at a hundred jiffies a second.
	const jiffies = secondsPerRound * 100

	m := host.Metrics{
		Load1: 7.21, Load5: 5.98, Load15: 4.55, CPUs: 8,
		Uptime:         42*24*time.Hour + 7*time.Hour,
		UptimeSeconds:  3654000 + float64(round*secondsPerRound),
		MemTotalKB:     32833536,
		MemAvailableKB: 3923456,
		SwapTotalKB:    8388608,
		SwapFreeKB:     5033165,
		DiskTotalKB:    205520896,
		DiskUsedKB:     93323264,
		Filesystems: []host.Filesystem{
			{Device: "/dev/nvme0n1p2", Mount: "/", TotalKB: 205520896, UsedKB: 93323264},
			{Device: "/dev/nvme0n1p1", Mount: "/boot", TotalKB: 1046528, UsedKB: 314572},
			{Device: "/dev/sdb1", Mount: "/var", TotalKB: 419430400, UsedKB: 390455296},
		},
		Pressure: host.PressureSet{
			CPU:     host.Pressure{Some10: 24.5},
			IO:      host.Pressure{Some10: 8.3, Full10: 2.1},
			Present: true,
		},
	}
	// Memory climbing towards trouble rather than sitting there, which is
	// the difference the trend beside it exists to show.
	m.MemAvailableKB = 12_000_000 - uint64(round)*260_000

	// One core pinned, one busy, the rest idling — which is the case a load
	// average of 7.21 over eight cores cannot distinguish from six cores at
	// nine tenths.
	busy := []float64{0.98, 0.61, 0.12, 0.09, 0.31, 0.04, 0.07, 0.02}
	machine := host.CPUTime{Name: "cpu"}
	var traffic uint64
	for core, fraction := range busy {
		var counter host.CPUTime
		// The counters are cumulative, so each round is added rather than
		// multiplied in: that is what gives the strip a shape instead of a
		// flat line at one value.
		for r := 1; r <= round; r++ {
			counter.Total += jiffies
			counter.Idle += uint64(float64(jiffies) * (1 - fraction*shotWave(r)))
		}
		counter.Name = fmt.Sprintf("cpu%d", core)
		m.CPUTimes = append(m.CPUTimes, counter)
		machine.Total += counter.Total
		machine.Idle += counter.Idle
	}
	m.CPUTimes = append([]host.CPUTime{machine}, m.CPUTimes...)
	for r := 1; r <= round; r++ {
		traffic += uint64(1_450_000 * shotWave(r))
	}
	m.Interfaces = []host.Interface{
		{Name: "lo", RxBytes: uint64(round) * 12_000, TxBytes: uint64(round) * 12_000},
		{Name: "eth0", RxBytes: traffic, TxBytes: traffic / 4},
		{Name: "veth3f1a", RxBytes: uint64(round) * 980_000, TxBytes: uint64(round) * 210_000},
	}
	return m
}

// shotWave is a deterministic stand-in for a machine doing something: the
// per-round multiplier that gives the sparklines a shape to be read.
func shotWave(round int) float64 {
	pattern := []float64{0.22, 0.35, 0.50, 0.78, 1.00, 0.92, 0.61, 0.40,
		0.28, 0.44, 0.70, 0.96, 0.52, 0.30}
	return pattern[(round-1)%len(pattern)]
}

// Every state the table has a color for, in one project.
func shotServices() []compose.Service {
	count := func(n int) *int { return &n }
	return []compose.Service{
		{Service: "nginx", Name: "myapp-nginx-1", ID: "id-nginx", Pid: 100,
			State: "running", Health: "healthy",
			Status: "Up 3 days (healthy)", Restarts: count(0),
			Publishers: []compose.Publisher{{PublishedPort: 443, TargetPort: 443, Protocol: "tcp"}}},
		{Service: "api", Name: "myapp-api-1", ID: "id-api", Pid: 101,
			State: "running", Health: "healthy",
			Status: "Up 3 days (healthy)", Restarts: count(2),
			Publishers: []compose.Publisher{{PublishedPort: 8080, TargetPort: 3000, Protocol: "tcp"}}},
		{Service: "postgres", Name: "myapp-postgres-1", ID: "id-postgres", Pid: 102,
			State: "running", Health: "starting",
			Status: "Up 4 seconds (health: starting)", Restarts: count(0)},
		{Service: "cache", Name: "myapp-cache-1", ID: "id-cache", Pid: 103,
			State: "running", Health: "unhealthy",
			Status: "Up 2 hours (unhealthy)", Restarts: count(0)},
		{Service: "migrate", Name: "myapp-migrate-1", State: "exited",
			Status: "Exited (0) 3 days ago", Restarts: count(0)},
		{Service: "worker", Name: "myapp-worker-1", State: "restarting",
			Status: "Restarting (137) 12 seconds ago", Restarts: count(7)},
	}
}

// shotCgroups is a pair of readings a few seconds apart, so the frames show
// what the second one derives: a CPU percentage per container, memory against
// the machine's, and the I/O totals.
func shotCgroups(round int) compose.CgroupSample {
	// Microseconds of CPU per round. Five seconds pass between rounds, so
	// each value is the percentage the frame should show times fifty
	// thousand: 5_617_000 reads as 112.34%, an api using more than a core.
	busy := map[string]uint64{"nginx": 7_500, "api": 5_617_000, "postgres": 151_000,
		"cache": 4_405_000}
	memory := map[string]uint64{"nginx": 12_939_428, "api": 1_325_142_016,
		"postgres": 886_244_147, "cache": 31_030_896_230}
	sample := compose.CgroupSample{
		At:         time.Date(2026, 9, 4, 12, 0, round*5, 0, time.UTC),
		Containers: map[string]compose.CgroupReading{},
		Networks:   map[int]compose.CgroupReading{},
	}
	for index, service := range []string{"nginx", "api", "postgres", "cache"} {
		sample.Containers["id-"+service] = compose.CgroupReading{
			CPUMicros:  busy[service] * uint64(round),
			MemBytes:   memory[service],
			ReadBytes:  4_100 + uint64(index)*1_230_000_000,
			WriteBytes: uint64(index) * 4_560_000_000,
			PIDs:       14,
		}
		sample.Networks[100+index] = compose.CgroupReading{
			RxBytes: 1_450_000_000 >> (index * 3), TxBytes: 892_300_000 >> (index * 2)}
	}
	return sample
}

// shotRounds is how many samples the frames are built from: enough for the
// sparklines to have something to draw, since a strip of two cells is not a
// trend.
const shotRounds = 18

// shotScreen assembles a home over a services panel filled with the fixtures
// above. hostErr makes the host fetch fail, which is how the stale flag is
// reached — through the panel's own path rather than by writing its fields.
func shotScreen(width, height int, hostMetrics bool, hostErr error) *Model {
	panel := status.New(status.Config{Stats: true, LiveStats: true})
	round, hostRound := 0, 0
	failing := false
	config := Config{
		Target:     "deploy@app-prod-01",
		ComposeDir: "/srv/myapp",
		Services:   func() ([]compose.Service, error) { return shotServices(), nil },
		Stats: func([]compose.Service) (compose.CgroupSample, error) {
			round++
			return shotCgroups(round), nil
		},
	}
	if hostMetrics {
		config.Host = func() (host.Metrics, error) {
			if failing {
				return host.Metrics{}, hostErr
			}
			hostRound++
			return busyHost(hostRound), nil
		}
	}
	screen := New(config, panel)
	screen.SetSize(width, height)
	sampleAll(screen)
	// The container counters have nothing to address until the first service
	// list has landed, and a percentage is the difference between two
	// readings — so the frames take three passes to show a full table, and
	// as many again to fill the sparklines beside the host readings.
	for range shotRounds {
		resample(screen)
	}
	if hostErr != nil {
		failing = true
		resample(screen)
	}
	return screen
}

func TestUIShot(t *testing.T) {
	path := os.Getenv("LINQODE_UI_SHOT")
	if path == "" {
		t.Skip("set LINQODE_UI_SHOT=<file.html> to render the screen in color")
	}
	// Force a profile: without a TTY lipgloss renders everything plain,
	// which is the whole reason this file exists.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	wide := shotScreen(150, 20, true, nil)
	wide.Update(key("j"))

	// Focus on the band: its label lights up and the table's box goes quiet,
	// which is the whole point of the ring having two stops.
	onBand := shotScreen(150, 20, true, nil)
	onBand.Update(key("j"))
	onBand.Update(tea.KeyMsg{Type: tea.KeyTab})

	systemView := shotScreen(150, 20, true, nil)
	systemView.Update(tea.KeyMsg{Type: tea.KeyTab})
	systemView.Update(tea.KeyMsg{Type: tea.KeyEnter})

	narrowSystem := shotScreen(100, 20, true, nil)
	narrowSystem.Update(tea.KeyMsg{Type: tea.KeyTab})
	narrowSystem.Update(tea.KeyMsg{Type: tea.KeyEnter})

	narrow := shotScreen(100, 20, true, nil)

	noMetrics := shotScreen(150, 20, false, nil)

	stale := shotScreen(150, 20, true, errors.New("dial tcp: i/o timeout"))

	frames := []shotFrame{
		{Name: "150 columns — every state, second row selected", Text: wide.View()},
		{Name: "150 columns — focus on the band, the table's box goes quiet",
			Text: onBand.View()},
		{Name: "150 columns — the system view, opened with enter on the band",
			Text: systemView.View()},
		{Name: "100 columns — the system view, where the rows have to give something up",
			Text: narrowSystem.View()},
		{Name: "100 columns — I/O columns dropped, gap narrowed", Text: narrow.View()},
		{Name: "150 columns — before the first host sample", Text: noMetrics.View()},
		{Name: "150 columns — host sample gone stale", Text: stale.View()},
	}
	if err := os.WriteFile(path, []byte(shotPage("Linqode home screen", frames)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

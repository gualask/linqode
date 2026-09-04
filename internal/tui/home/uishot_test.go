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

func busyHost() host.Metrics {
	return host.Metrics{
		Load1: 7.21, Load5: 5.98, Load15: 4.55, CPUs: 8,
		Uptime:         42*24*time.Hour + 7*time.Hour,
		MemTotalKB:     32833536,
		MemAvailableKB: 3923456,
		DiskTotalKB:    205520896,
		DiskUsedKB:     93323264,
	}
}

// Every state the table has a color for, in one project.
func shotServices() []compose.Service {
	count := func(n int) *int { return &n }
	return []compose.Service{
		{Service: "nginx", Name: "myapp-nginx-1", State: "running", Health: "healthy",
			Status: "Up 3 days (healthy)", Restarts: count(0),
			Publishers: []compose.Publisher{{PublishedPort: 443, TargetPort: 443, Protocol: "tcp"}}},
		{Service: "api", Name: "myapp-api-1", State: "running", Health: "healthy",
			Status: "Up 3 days (healthy)", Restarts: count(2),
			Publishers: []compose.Publisher{{PublishedPort: 8080, TargetPort: 3000, Protocol: "tcp"}}},
		{Service: "postgres", Name: "myapp-postgres-1", State: "running", Health: "starting",
			Status: "Up 4 seconds (health: starting)", Restarts: count(0)},
		{Service: "cache", Name: "myapp-cache-1", State: "running", Health: "unhealthy",
			Status: "Up 2 hours (unhealthy)", Restarts: count(0)},
		{Service: "migrate", Name: "myapp-migrate-1", State: "exited",
			Status: "Exited (0) 3 days ago", Restarts: count(0)},
		{Service: "worker", Name: "myapp-worker-1", State: "restarting",
			Status: "Restarting (137) 12 seconds ago", Restarts: count(7)},
	}
}

func shotReadings() []compose.ContainerStats {
	reading := func(name, cpu, mem, net, block string) compose.ContainerStats {
		return compose.ContainerStats{Name: name, CPUPerc: cpu, MemUsage: mem,
			MemPerc: "12.5%", NetIO: net, BlockIO: block, PIDs: "14"}
	}
	return []compose.ContainerStats{
		reading("myapp-nginx-1", "0.15%", "12.34MiB / 31.31GiB", "1.45GB / 892.3MB", "4.1kB / 0B"),
		reading("myapp-api-1", "112.34%", "1.234GiB / 31.31GiB", "892.3MB / 1.45GB", "1.23GB / 4.56GB"),
		reading("myapp-postgres-1", "3.02%", "845.2MiB / 31.31GiB", "12.3kB / 8.9kB", "45.6MB / 12.3GB"),
		reading("myapp-cache-1", "88.10%", "28.9GiB / 31.31GiB", "5.2MB / 3.1MB", "900MB / 1.1GB"),
	}
}

// shotScreen assembles a home over a services panel filled with the fixtures
// above. hostErr makes the host fetch fail, which is how the stale flag is
// reached — through the panel's own path rather than by writing its fields.
func shotScreen(width, height int, hostMetrics bool, hostErr error) *Model {
	panel := status.New(status.Config{
		Services:  func() ([]compose.Service, error) { return shotServices(), nil },
		Stats:     func() ([]compose.ContainerStats, error) { return shotReadings(), nil },
		LiveStats: true,
	})
	feed := &hostFeed{metrics: busyHost()}
	config := Config{Target: "deploy@app-prod-01", ComposeDir: "/srv/myapp"}
	if hostMetrics {
		config.Host = feed.sample
	}
	screen := New(config, panel)
	screen.SetSize(width, height)
	sampleAll(screen, panel)
	if hostErr != nil {
		feed.err = hostErr
		sampleAll(screen, panel)
	}
	return screen
}

// apply runs a command the way the Bubble Tea loop would, feeding every
// message it produces back into the panel. Sample is used rather than Init
// because Init also arms the refresh timers, and a tick command run inline
// would sleep for its whole interval.
func apply(panel *status.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, sub := range msg {
			apply(panel, sub)
		}
	default:
		apply(panel, panel.Update(msg))
	}
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

	narrow := shotScreen(100, 20, true, nil)

	noMetrics := shotScreen(150, 20, false, nil)

	stale := shotScreen(150, 20, true, errors.New("dial tcp: i/o timeout"))

	frames := []shotFrame{
		{Name: "150 columns — every state, second row selected", Text: wide.View()},
		{Name: "150 columns — focus on the band, the table's box goes quiet",
			Text: onBand.View()},
		{Name: "150 columns — the system view, opened with enter on the band",
			Text: systemView.View()},
		{Name: "100 columns — I/O columns dropped, gap narrowed", Text: narrow.View()},
		{Name: "150 columns — before the first host sample", Text: noMetrics.View()},
		{Name: "150 columns — host sample gone stale", Text: stale.View()},
	}
	if err := os.WriteFile(path, []byte(shotPage("Linqode home screen", frames)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

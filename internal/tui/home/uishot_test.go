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
// and the assertions stay in the tests beside it. It lives in _test.go
// files rather than a package of its own precisely because it is not
// production code: the compiler leaves it out of every ordinary build.
//
//	LINQODE_UI_SHOT=/tmp/shot.html go test ./internal/tui/home/ -run TestUIShot
//
// Without the variable the test skips, so a normal run costs nothing.

import (
	"errors"
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/follow"
	"github.com/gualask/linqode/internal/tui/status"
)

// shotRounds is how many samples the frames are built from: enough for the
// sparklines to have something to draw, since a strip of two cells is not a
// trend.
const shotRounds = 18

// shotScreen assembles a home over a services panel filled with fixture data.
// hostErr makes the host fetch fail, which is how the stale flag is reached —
// through the panel's own path rather than by writing its fields.
func shotScreen(width, height int, hostMetrics bool, hostErr error) *Model {
	panel := status.New(status.Config{Stats: true, LiveStats: true})
	round, hostRound, processRound := 0, 0, 0
	failing := false
	stream := make(chan operations.Event, 32)
	config := Config{
		Target:     "deploy@app-prod-01",
		ComposeDir: "/srv/myapp",
		Services:   func() ([]compose.Service, error) { return shotServices(), nil },
		Watch: func(string) (operations.Feed, error) {
			return operations.Feed{Events: stream, Stop: func() {}}, nil
		},
		Processes: func() (host.ProcessSample, error) {
			processRound++
			return busyProcesses(processRound), nil
		},
		GPUs: func() ([]host.GPU, error) {
			// One card of each vendor, which is not a machine anyone has —
			// it is the frame that shows both shapes at once.
			return host.ParseGPUs([]byte("#amdgpu\n" +
				"/sys/class/drm/card0/device/gpu_busy_percent:62\n" +
				"/sys/class/drm/card0/device/mem_info_vram_used:5368709120\n" +
				"/sys/class/drm/card0/device/mem_info_vram_total:17179869184\n" +
				"/sys/class/drm/card0/device/hwmon/hwmon4/temp1_input:68000\n" +
				"/sys/class/drm/card0/device/hwmon/hwmon4/power1_average:184000000\n" +
				"#nvidia\n0, NVIDIA A10, 91, 21402, 23028, 74, 148.6\n")), nil
		},
		DiskUsage: func() ([]compose.DiskUsage, error) {
			// A daemon holding rather more than anyone meant it to, which
			// is the state this row exists to make visible.
			return compose.ParseSystemDF([]byte(
				"Images|31|6|48.21GB|31.42GB (65%)\n" +
					"Containers|12|4|1.204GB|402.7MB (33%)\n" +
					"Local Volumes|4|4|8.914GB|0B\n" +
					"Build Cache|118|0|6.117GB|6.117GB (100%)\n")), nil
		},
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
	for _, event := range shotEvents() {
		stream <- operations.Event{Kind: operations.EventChange, Change: event}
	}
	applyScreen(screen, screen.handleWatchTick())
	return screen
}

// shotUnavailable is the host the probe found without a usable compose. Every
// machine reading is there; the table is one sentence saying why it is not.
//
// It is here to be looked at rather than asserted on, because what can go
// wrong with it is a colour: this frame must not read as a failure. Nothing
// failed — the host simply is what it is — and a panel dressed in the red the
// stale meters and the refresh errors use would say the opposite.
func shotUnavailable(width, height int, reason string) *Model {
	services := status.New(status.Config{Unavailable: reason})
	hostRound, processRound := 0, 0
	screen := New(Config{
		Target:             "deploy@app-prod-01",
		ComposeDir:         "/srv/myapp",
		OS:                 "Debian GNU/Linux 12 (bookworm)",
		ComposeUnavailable: reason,
		Host: func() (host.Metrics, error) {
			hostRound++
			return busyHost(hostRound), nil
		},
		Processes: func() (host.ProcessSample, error) {
			processRound++
			return busyProcesses(processRound), nil
		},
	}, services)
	screen.SetSize(width, height)
	sampleAll(screen)
	for range shotRounds {
		applyScreen(screen, screen.sampler.read(sourceHost))
	}
	return screen
}

// openSystem walks into the system view the way an operator does, and takes
// the second process reading a CPU share needs to exist. Opening it is what
// asks for the first: the source is gated on being there.
func openSystem(screen *Model) {
	openSystemView(screen)
	applyScreen(screen, screen.sampler.read(sourceProcesses))
}

func shotFollow(width int, structured, ended bool) string {
	events := make(chan operations.Event, 4)
	lines := []string{
		"Preparing deployment",
		"Traceback (most recent call last):",
		"\x1b]52;c;Y2xpcGJvYXJk\x07ConnectionError: database unavailable",
	}
	if structured {
		lines = []string{
			`{"timestamp":"12:00:01","level":"info","msg":"Preparing deployment","source":"deploy"}`,
			`{"timestamp":"12:00:02","level":"warn","msg":"Database connection delayed","source":"deploy"}`,
			`{"timestamp":"12:00:03","level":"error","msg":"\u001b]52;c;Y2xpcGJvYXJk\u0007Database unavailable","source":"deploy"}`,
		}
	}
	for i, line := range lines {
		kind := operations.EventStdout
		if i > 0 {
			kind = operations.EventStderr
		}
		events <- operations.Event{Kind: kind, Text: line}
	}
	if ended {
		events <- operations.Event{Kind: operations.EventExit, ExitCode: 1}
		close(events)
	}
	m := follow.New("deploy@prod", "script: deploy", operations.Feed{Events: events, Stop: func() {}})
	m.SetSize(width, 12)
	m.Update(m.Init()())
	return m.View()
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

	// Focus starts on the header. This is the table holding it instead, one
	// tab forward, with a row selected.
	wide := shotScreen(150, 20, true, nil)
	wide.Update(tea.KeyMsg{Type: tea.KeyTab})
	wide.Update(tea.KeyMsg{Type: tea.KeyDown})

	// Where the session opens: the header lit, the table quiet. It is a box
	// like every other region now, so this frame is here to confirm it reads
	// as one — the accent on a border and a title, as everywhere else.
	onHeader := shotScreen(150, 20, true, nil)

	// Focus on the band: its label lights up and the table's box goes quiet,
	// which is the whole point of the ring having two stops.

	systemView := shotScreen(150, 24, true, nil)
	openSystem(systemView)

	// Walked to rather than counted to: one `tab` from the header is the
	// table, and this frame is about the region after it — where `enter`
	// opens the same logs and `c` now acts on the container the event was
	// about rather than on the table's selection.
	onFeed := shotScreen(150, 24, true, nil)
	focusPanel(onFeed, "events")
	onFeed.Update(tea.KeyMsg{Type: tea.KeyDown})

	short := shotScreen(150, 14, true, nil)

	byCPUView := shotScreen(150, 24, true, nil)
	openSystem(byCPUView)
	byCPUView.Update(key("s"))

	narrowSystem := shotScreen(100, 20, true, nil)
	openSystem(narrowSystem)

	narrow := shotScreen(100, 20, true, nil)

	noMetrics := shotScreen(150, 20, false, nil)

	stale := shotScreen(150, 20, true, errors.New("dial tcp: i/o timeout"))

	denied := shotUnavailable(150, 20,
		"the docker daemon refuses this user — not in the `docker` group?")
	// The same finding on a host that also has host_metrics off: nothing
	// behind the band either, so the panel that at least says why keeps the
	// body.
	noMachine := status.New(status.Config{
		Unavailable: "this host has docker-compose v1, which linqode does not drive"})
	bare := New(Config{Target: "deploy@old-box", ComposeDir: "/srv/myapp",
		ComposeUnavailable: "this host has docker-compose v1, which linqode does not drive",
	}, noMachine)
	bare.SetSize(150, 20)

	frames := []shotFrame{
		{Name: "120 columns — running script with stdout and stderr", Text: shotFollow(120, false, false)},
		{Name: "120 columns — structured script output with terminal controls removed", Text: shotFollow(120, true, false)},
		{Name: "80 columns — failed script retains the traceback", Text: shotFollow(80, false, true)},
		{Name: "150 columns — where the session opens: focus on the header",
			Text: onHeader.View()},
		{Name: "150 columns — one tab forward, the table with a row selected",
			Text: wide.View()},
		{Name: "150 columns — the system view, opened with enter on the band",
			Text: systemView.View()},
		{Name: "150 columns — the same view, ranked by CPU", Text: byCPUView.View()},
		{Name: "150x24 — focus on the feed, second event selected", Text: onFeed.View()},
		{Name: "150x14 — too short for both: the satellite gives its rows back",
			Text: short.View()},
		{Name: "100 columns — the system view, where the rows have to give something up",
			Text: narrowSystem.View()},
		{Name: "100 columns — I/O columns dropped, gap narrowed", Text: narrow.View()},
		{Name: "150 columns — before the first host sample", Text: noMetrics.View()},
		{Name: "150 columns — host sample gone stale", Text: stale.View()},
		{Name: "150 columns — no compose on this host: the machine takes the body",
			Text: denied.View()},
		{Name: "150 columns — no compose and no host metrics either: nothing left to promote",
			Text: bare.View()},
	}
	if err := os.WriteFile(path, []byte(shotPage("Linqode home screen", frames)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

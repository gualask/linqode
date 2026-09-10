package home

// Tests for what the screen owns: opening a service, the two modal menus, the
// `!` prompt, and the footer. They moved here with that ownership — the status
// package used to be the screen, and these were its tests.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/status"
)

func serviceList(names ...string) []compose.Service {
	list := make([]compose.Service, len(names))
	for i, name := range names {
		list[i] = compose.Service{
			Service: name,
			Name:    "app-" + name + "-1",
			State:   "running",
			Status:  "Up",
		}
	}
	return list
}

func key(k string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// screenWith builds a home over a services panel already holding names. The
// panel is filled through its own fetch rather than by reaching into it: the
// screen only ever sees a panel the way the application hands it over.
func screenWith(t *testing.T, config Config, names ...string) *Model {
	t.Helper()
	config.Services = func() ([]compose.Service, error) { return serviceList(names...), nil }
	screen := New(config, status.New(status.Config{}))
	sampleAll(screen)
	return screen
}

func requestMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command produced")
	}
	return cmd()
}

func TestEnterOpensLogsForSelectedService(t *testing.T) {
	m := screenWith(t, Config{ComposeDir: "/srv/app"}, "db", "web")
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	msg, ok := requestMsg(t, m.Update(tea.KeyMsg{Type: tea.KeyEnter})).(OpenLogsMsg)
	if !ok || msg.Title != "logs: web" || msg.Service != "web" {
		t.Fatalf("log request = %+v", msg)
	}
}

func TestActionMenuTargetsSelectedService(t *testing.T) {
	config := Config{ComposeDir: "/srv/app"}
	config.ActionPreview = func(action operations.ServiceAction, service string) string {
		return compose.ActionCommand(config.ComposeDir, action, service)
	}
	m := screenWith(t, config, "db", "web")
	m.SetSize(120, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(key("c"))
	if m.menu == nil {
		t.Fatal("c should open the action menu")
	}
	for _, want := range []string{"restart web", "stop web", "start web", "docker compose restart 'web'"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("menu missing %q:\n%s", want, m.View())
		}
	}
	assertActionRequest(t, m, operations.ActionRestart, "restart: web", 0)
	assertActionRequest(t, m, operations.ActionStop, "stop: web", 1)
	assertActionRequest(t, m, operations.ActionStart, "start: web", 2)

	empty := screenWith(t, Config{})
	empty.Update(key("c"))
	if empty.menu != nil {
		t.Error("c with no services should not open a menu")
	}
}

// `c` acts on a selection, and a selection belongs to a region. It used to be
// a screen key beside `r` and `x`, and read the table's selection from
// wherever it was pressed: a lifecycle menu about a service chosen somewhere
// else, and — from the system view, which takes the whole body — about a
// service that was not on the screen at all.
func TestActionsBelongToTheFocusedRegion(t *testing.T) {
	screen, _ := buildScreen(screenOptions{width: 150, height: 24,
		services: serviceList("web"), host: &hostFeed{metrics: sampleMetrics()}})
	sampleAll(screen)

	// Focus starts on the machine, which has no service under its cursor.
	focusPanel(screen, "system")
	if cmd := screen.Update(key("c")); cmd != nil || screen.menu != nil {
		t.Error("c opened an action menu from the header band")
	}
	if strings.Contains(screen.View(), "c actions") {
		t.Errorf("the action key is advertised on the band:\n%s", screen.View())
	}

	// One `tab` away it is the table's, and it says so.
	focusPanel(screen, "services")
	if !strings.Contains(screen.View(), "c actions") {
		t.Errorf("the action key is not offered on the table:\n%s", screen.View())
	}
	screen.Update(key("c"))
	if screen.menu == nil {
		t.Fatal("c did not open the action menu on the table")
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// And inside the machine's readings, where the table is not drawn at all,
	// it is neither offered nor answered.
	openSystemView(screen)
	if screen.detail == nil {
		t.Fatal("the system view did not open")
	}
	if cmd := screen.Update(key("c")); cmd != nil || screen.menu != nil {
		t.Error("c opened an action menu from inside the system view")
	}
	if strings.Contains(screen.View(), "c actions") {
		t.Errorf("the action key is advertised inside the system view:\n%s", screen.View())
	}
}

// On the feed, `c` acts on the container the selected event was about. The
// feed's entries are containers too, and asking what happened to one and then
// restarting it is one region's worth of context — not the table's, which is
// sitting on a different service.
func TestActionsOnTheFeedTargetTheEventsContainer(t *testing.T) {
	stream := newWatchStream()
	screen := New(Config{
		Services: func() ([]compose.Service, error) {
			return []compose.Service{
				{Service: "db", Name: "app-db-1", Project: "app", State: "running"},
				{Service: "web", Name: "app-web-1", Project: "app", State: "running"},
			}, nil
		},
		Watch: func(string) (operations.Feed, error) { return stream.feed(), nil },
	}, status.New(status.Config{}))
	screen.SetSize(150, 30)
	applyScreen(screen, screen.sampler.due())

	stream.change("app-web-1")
	applyScreen(screen, screen.handleWatchTick())

	// The table is on the first service; the event is about the second.
	if service, _ := screen.services.SelectedService(); service != "db" {
		t.Fatalf("the table selects %q, want the first service", service)
	}
	focusPanel(screen, "events")
	if title := screen.focused().Title(); title != "events" {
		t.Fatalf("focus is on %q, want the feed", title)
	}
	screen.Update(key("c"))
	if screen.menu == nil {
		t.Fatal("c did not open the action menu on the feed")
	}
	msg, ok := requestMsg(t, screen.Update(tea.KeyMsg{Type: tea.KeyEnter})).(OpenActionMsg)
	if !ok || msg.Service != "web" {
		t.Errorf("action request = %+v, want the container the event was about", msg)
	}
}

// An event about a container the project no longer has names nothing that can
// be restarted, which is the condition `enter` already meets there.
func TestActionsOnAnEventWithNoContainerOpenNothing(t *testing.T) {
	stream := newWatchStream()
	screen, _ := watched(t, stream)
	screen.SetSize(150, 30)

	stream.change("app-gone-1")
	applyScreen(screen, screen.handleWatchTick())
	focusPanel(screen, "events")
	if title := screen.focused().Title(); title != "events" {
		t.Fatalf("focus is on %q, want the feed", title)
	}
	if service, ok := screen.events.SelectedService(); ok {
		t.Fatalf("the feed named %q for a container the project does not have", service)
	}
	if cmd := screen.Update(key("c")); cmd != nil || screen.menu != nil {
		t.Error("c opened a menu for an event about a container that is gone")
	}
}

func assertActionRequest(t *testing.T, screen *Model, action operations.ServiceAction, title string, down int) {
	t.Helper()
	if screen.menu == nil {
		screen.Update(key("c"))
	}
	for range down {
		screen.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	msg, ok := requestMsg(t, screen.Update(tea.KeyMsg{Type: tea.KeyEnter})).(OpenActionMsg)
	if !ok || msg.Title != title || msg.Service != "web" || msg.Action != action {
		t.Errorf("action request = %+v", msg)
	}
}

func TestNoActionKeysDifferOnlyByCase(t *testing.T) {
	m := screenWith(t, Config{ComposeDir: "/srv/app"}, "web")
	for _, candidate := range []string{"R", "S", "C", "X", "s"} {
		if cmd := m.Update(key(candidate)); cmd != nil || m.menu != nil {
			t.Errorf("%q still triggers an action", candidate)
		}
	}
}

func TestScriptsMenuRunsSelectedScript(t *testing.T) {
	config := Config{Scripts: []operations.Script{{Name: "disk", Command: "df -h"},
		{Name: "mem", Command: "free -m"}}}
	m := screenWith(t, config, "web")
	m.SetSize(120, 30)
	m.Update(key("x"))
	if m.menu == nil || m.menu.selected != 0 {
		t.Fatalf("menu not open: %+v", m.menu)
	}
	if view := m.View(); !strings.Contains(view, "disk") || !strings.Contains(view, "free -m") {
		t.Errorf("menu content missing:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	msg, ok := requestMsg(t, m.Update(tea.KeyMsg{Type: tea.KeyEnter})).(OpenScriptMsg)
	if !ok || msg.Title != "script: mem" || msg.Name != "mem" || m.menu != nil {
		t.Errorf("script request = %+v; menu = %+v", msg, m.menu)
	}
	m.Update(key("x"))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu != nil {
		t.Error("menu should close on esc")
	}
	m.Update(key("x"))
	m.Update(key("x"))
	if m.menu != nil {
		t.Error("the opening key should close the menu again")
	}
}

func TestFooterFitsTheTerminal(t *testing.T) {
	m := screenWith(t, Config{Target: "deploy@prod"}, "web", "db")
	m.SetSize(70, 20)
	for index, line := range strings.Split(m.View(), "\n") {
		if width := lipgloss.Width(line); width > 70 {
			t.Errorf("line %d is %d columns wide: %q", index, width, line)
		}
	}
	if !strings.Contains(m.View(), "q quit") {
		t.Errorf("footer lost the quit hint:\n%s", m.View())
	}
}

func TestScriptsKeyWithoutScriptsShowsError(t *testing.T) {
	m := screenWith(t, Config{}, "web")
	m.SetSize(120, 30)
	m.Update(key("x"))
	if m.menu != nil || !strings.Contains(m.View(), "no scripts configured") {
		t.Errorf("missing-script state is incorrect:\n%s", m.View())
	}
}

func TestAdHocCommandPromptRunsWhatWasTyped(t *testing.T) {
	m := screenWith(t, Config{}, "web")
	m.SetSize(120, 30)
	m.Update(key("!"))
	if !m.commandPrompt {
		t.Fatal("! should open the prompt")
	}
	// While the prompt is open the keys type instead of acting: `q` must
	// not quit, `j` must not move the selection.
	for _, k := range []string{"q", "j", " ", "-", "h"} {
		if cmd := m.Update(key(k)); cmd != nil {
			t.Fatalf("key %q acted while typing", k)
		}
	}
	if m.commandText != "qj -h" {
		t.Errorf("typed text %q", m.commandText)
	}
	if !strings.Contains(m.View(), "$ qj -h") {
		t.Errorf("prompt not shown:\n%s", m.View())
	}

	cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should run the command")
	}
	msg, ok := cmd().(OpenAdHocMsg)
	if !ok {
		t.Fatalf("got %T", cmd())
	}
	// Sent as typed: what the user writes runs where plain ssh would run
	// it, not inside the compose directory.
	if msg.Command != "qj -h" || msg.Title != "$ qj -h" {
		t.Errorf("%+v", msg)
	}
	if m.commandPrompt {
		t.Error("prompt should close after running")
	}
}

func TestAdHocPromptCancelsAndRemembers(t *testing.T) {
	m := screenWith(t, Config{}, "web", "db")

	// An empty command is a no-op, not a remote `sh -c ''`.
	m.Update(key("!"))
	if cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("empty command should not run")
	}

	m.Update(key("!"))
	m.Update(key("d"))
	m.Update(key("f"))
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m.Update(key("h"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Esc cancels without running, and the prompt reopens on the last
	// command so a typo is edited rather than retyped.
	m.Update(key("!"))
	if m.commandText != "dh" {
		t.Errorf("prompt reopened with %q, want the last command", m.commandText)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.commandPrompt {
		t.Error("esc should close the prompt")
	}
	// Keys reach the panel again: the cursor moves.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if service, _ := m.services.SelectedService(); service != "db" {
		t.Errorf("selection is %q, want the keys to act again after esc", service)
	}
}

// `l` was an alias for enter, and is gone with the rest of them.
func TestLNoLongerDescends(t *testing.T) {
	m := screenWith(t, Config{}, "web")
	m.SetSize(120, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyTab}) // onto the table
	if cmd := m.Update(key("l")); cmd != nil {
		t.Errorf("l still opened something: %#v", cmd())
	}
	if cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("enter no longer opens the logs")
	}
}

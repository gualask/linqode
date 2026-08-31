package status

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

func requestMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command produced")
	}
	return cmd()
}

func TestEnterOpensLogsForSelectedService(t *testing.T) {
	m := New(Config{ComposeDir: "/srv/app"})
	m.Update(servicesMsg{services: services("db", "web")})
	m.Update(key("j"))
	msg, ok := requestMsg(t, m.Update(tea.KeyMsg{Type: tea.KeyEnter})).(OpenLogsMsg)
	if !ok || msg.Title != "logs: web" || msg.Service != "web" {
		t.Fatalf("log request = %+v", msg)
	}
}

func TestActionMenuTargetsSelectedService(t *testing.T) {
	m := New(Config{ComposeDir: "/srv/app"})
	m.actionPreview = func(action operations.ServiceAction, service string) string {
		return compose.ActionCommand(m.info.ComposeDir, action, service)
	}
	m.Update(servicesMsg{services: services("db", "web")})
	m.Update(key("j"))
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
	empty := New(Config{})
	empty.Update(servicesMsg{services: nil})
	empty.Update(key("c"))
	if empty.menu != nil {
		t.Error("c with no services should not open a menu")
	}
}

func assertActionRequest(t *testing.T, model *Model, action operations.ServiceAction, title string, down int) {
	t.Helper()
	if model.menu == nil {
		model.Update(key("c"))
	}
	for range down {
		model.Update(key("j"))
	}
	msg, ok := requestMsg(t, model.Update(tea.KeyMsg{Type: tea.KeyEnter})).(OpenActionMsg)
	if !ok || msg.Title != title || msg.Service != "web" || msg.Action != action {
		t.Errorf("action request = %+v", msg)
	}
}

func TestNoActionKeysDifferOnlyByCase(t *testing.T) {
	m := New(Config{ComposeDir: "/srv/app"})
	m.Update(servicesMsg{services: services("web")})
	for _, candidate := range []string{"R", "S", "C", "X", "s"} {
		if cmd := m.Update(key(candidate)); cmd != nil || m.menu != nil {
			t.Errorf("%q still triggers an action", candidate)
		}
	}
}

func TestScriptsMenuRunsSelectedScript(t *testing.T) {
	info := Config{Scripts: []operations.Script{{Name: "disk", Command: "df -h"},
		{Name: "mem", Command: "free -m"}}}
	m := New(info)
	m.Update(servicesMsg{services: services("web")})
	m.Update(key("x"))
	if m.menu == nil || m.menu.selected != 0 {
		t.Fatalf("menu not open: %+v", m.menu)
	}
	if view := m.View(); !strings.Contains(view, "disk") || !strings.Contains(view, "free -m") {
		t.Errorf("menu content missing:\n%s", view)
	}
	m.Update(key("j"))
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
	m := New(Config{Target: "deploy@prod"})
	m.SetSize(70, 20)
	m.Update(servicesMsg{services: services("web", "db")})
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
	m := New(Config{})
	m.Update(servicesMsg{services: services("web")})
	m.Update(key("x"))
	if m.menu != nil || !strings.Contains(m.View(), "no scripts configured") {
		t.Errorf("missing-script state is incorrect:\n%s", m.View())
	}
}

package tui

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
	m := newStatusModel(Info{ComposeDir: "/srv/app"}, nil)
	m.update(servicesMsg{services: services("db", "web")})
	m.update(key("j"))
	msg, ok := requestMsg(t, m.update(tea.KeyMsg{Type: tea.KeyEnter})).(openLogsMsg)
	if !ok || msg.title != "logs: web" || msg.service != "web" {
		t.Fatalf("log request = %+v", msg)
	}
}

func TestActionMenuTargetsSelectedService(t *testing.T) {
	m := newStatusModel(Info{ComposeDir: "/srv/app"}, nil)
	m.actionPreview = func(action operations.ServiceAction, service string) string {
		return compose.ActionCommand(m.info.ComposeDir, action, service)
	}
	m.update(servicesMsg{services: services("db", "web")})
	m.update(key("j"))
	m.update(key("c"))
	if m.menu == nil {
		t.Fatal("c should open the action menu")
	}
	for _, want := range []string{"restart web", "stop web", "start web", "docker compose restart 'web'"} {
		if !strings.Contains(m.view(), want) {
			t.Errorf("menu missing %q:\n%s", want, m.view())
		}
	}
	assertActionRequest(t, &m, operations.ActionRestart, "restart: web", 0)
	assertActionRequest(t, &m, operations.ActionStop, "stop: web", 1)
	assertActionRequest(t, &m, operations.ActionStart, "start: web", 2)
	empty := newStatusModel(Info{}, nil)
	empty.update(servicesMsg{services: nil})
	empty.update(key("c"))
	if empty.menu != nil {
		t.Error("c with no services should not open a menu")
	}
}

func assertActionRequest(t *testing.T, model *statusModel, action operations.ServiceAction, title string, down int) {
	t.Helper()
	if model.menu == nil {
		model.update(key("c"))
	}
	for range down {
		model.update(key("j"))
	}
	msg, ok := requestMsg(t, model.update(tea.KeyMsg{Type: tea.KeyEnter})).(openActionMsg)
	if !ok || msg.title != title || msg.service != "web" || msg.action != action {
		t.Errorf("action request = %+v", msg)
	}
}

func TestNoActionKeysDifferOnlyByCase(t *testing.T) {
	m := newStatusModel(Info{ComposeDir: "/srv/app"}, nil)
	m.update(servicesMsg{services: services("web")})
	for _, candidate := range []string{"R", "S", "C", "X", "s"} {
		if cmd := m.update(key(candidate)); cmd != nil || m.menu != nil {
			t.Errorf("%q still triggers an action", candidate)
		}
	}
}

func TestScriptsMenuRunsSelectedScript(t *testing.T) {
	info := Info{Scripts: []operations.Script{{Name: "disk", Command: "df -h"},
		{Name: "mem", Command: "free -m"}}}
	m := newStatusModel(info, nil)
	m.update(servicesMsg{services: services("web")})
	m.update(key("x"))
	if m.menu == nil || m.menu.selected != 0 {
		t.Fatalf("menu not open: %+v", m.menu)
	}
	if view := m.view(); !strings.Contains(view, "disk") || !strings.Contains(view, "free -m") {
		t.Errorf("menu content missing:\n%s", view)
	}
	m.update(key("j"))
	msg, ok := requestMsg(t, m.update(tea.KeyMsg{Type: tea.KeyEnter})).(openScriptMsg)
	if !ok || msg.title != "script: mem" || msg.name != "mem" || m.menu != nil {
		t.Errorf("script request = %+v; menu = %+v", msg, m.menu)
	}
	m.update(key("x"))
	m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu != nil {
		t.Error("menu should close on esc")
	}
	m.update(key("x"))
	m.update(key("x"))
	if m.menu != nil {
		t.Error("the opening key should close the menu again")
	}
}

func TestFooterFitsTheTerminal(t *testing.T) {
	m := newStatusModel(Info{Target: "deploy@prod"}, nil)
	m.setSize(70, 20)
	m.update(servicesMsg{services: services("web", "db")})
	for index, line := range strings.Split(m.view(), "\n") {
		if width := lipgloss.Width(line); width > 70 {
			t.Errorf("line %d is %d columns wide: %q", index, width, line)
		}
	}
	if !strings.Contains(m.view(), "q quit") {
		t.Errorf("footer lost the quit hint:\n%s", m.view())
	}
}

func TestScriptsKeyWithoutScriptsShowsError(t *testing.T) {
	m := newStatusModel(Info{}, nil)
	m.update(servicesMsg{services: services("web")})
	m.update(key("x"))
	if m.menu != nil || !strings.Contains(m.view(), "no scripts configured") {
		t.Errorf("missing-script state is incorrect:\n%s", m.view())
	}
}

package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/follow"
	"github.com/gualask/linqode/internal/tui/home"
	"github.com/gualask/linqode/internal/tui/status"
)

func TestAppRoutesLogsThroughTypedBackend(t *testing.T) {
	var gotService string
	var gotTail int
	backend := Backend{
		Logs: func(service string, tail int) (operations.Feed, error) {
			gotService, gotTail = service, tail
			return closedFeed(), nil
		},
		AdHoc: func(string) (operations.Feed, error) {
			t.Fatal("log request used ad-hoc execution")
			return operations.Feed{}, nil
		},
	}
	model := newTestApp(backend)

	_, command := model.Update(home.OpenLogsMsg{Title: "logs: web", Service: "web"})
	message, ok := command().(feedMsg)
	if !ok || message.err != nil {
		t.Fatalf("message = %#v", command())
	}
	if gotService != "web" || gotTail != logTail {
		t.Fatalf("Logs(%q, %d)", gotService, gotTail)
	}
}

func TestAppRoutesActionsThroughTypedBackend(t *testing.T) {
	var gotAction operations.ServiceAction
	var gotService string
	backend := Backend{
		Action: func(action operations.ServiceAction, service string) (operations.Feed, error) {
			gotAction, gotService = action, service
			return closedFeed(), nil
		},
		AdHoc: func(string) (operations.Feed, error) {
			t.Fatal("action request used ad-hoc execution")
			return operations.Feed{}, nil
		},
	}
	model := newTestApp(backend)

	_, command := model.Update(home.OpenActionMsg{
		Title: "restart: web", Action: operations.ActionRestart, Service: "web",
	})
	message, ok := command().(feedMsg)
	if !ok || message.err != nil {
		t.Fatalf("message = %#v", command())
	}
	if gotAction != operations.ActionRestart || gotService != "web" {
		t.Fatalf("Action(%v, %q)", gotAction, gotService)
	}
}

func TestAppRoutesScriptsByConfiguredName(t *testing.T) {
	var gotName string
	backend := Backend{
		Script: func(name string) (operations.Feed, error) {
			gotName = name
			return closedFeed(), nil
		},
		AdHoc: func(string) (operations.Feed, error) {
			t.Fatal("script request used ad-hoc execution")
			return operations.Feed{}, nil
		},
	}
	model := newTestApp(backend)

	_, command := model.Update(home.OpenScriptMsg{Title: "script: deploy", Name: "deploy"})
	message, ok := command().(feedMsg)
	if !ok || message.err != nil {
		t.Fatalf("message = %#v", command())
	}
	if gotName != "deploy" {
		t.Fatalf("Script(%q)", gotName)
	}
}

func TestAppKeepsHumanCommandsOnAdHocBackend(t *testing.T) {
	var gotCommand string
	backend := Backend{
		AdHoc: func(command string) (operations.Feed, error) {
			gotCommand = command
			return closedFeed(), nil
		},
	}
	model := newTestApp(backend)

	_, command := model.Update(home.OpenAdHocMsg{Title: "$ uptime", Command: "uptime"})
	message, ok := command().(feedMsg)
	if !ok || message.err != nil {
		t.Fatalf("message = %#v", command())
	}
	if gotCommand != "uptime" {
		t.Fatalf("AdHoc(%q)", gotCommand)
	}
}

func TestAppRoutesLiveStatsThroughTypedBackend(t *testing.T) {
	called := false
	model := appModel{backend: Backend{
		LiveStats: func() (operations.Feed, error) {
			called = true
			return closedFeed(), nil
		},
	}}

	_, command := model.Update(status.OpenStatsMsg{RequestID: 42})
	message, ok := command().(status.StatsFeedMsg)
	if !ok || message.Err != nil {
		t.Fatalf("message = %#v", command())
	}
	if !called || message.RequestID != 42 {
		t.Fatal("LiveStats did not preserve the request identity")
	}
}

func TestAppSerializesFollowStarts(t *testing.T) {
	requests := []tea.Msg{
		home.OpenLogsMsg{Title: "logs: web", Service: "web"},
		home.OpenActionMsg{Title: "restart: web", Action: operations.ActionRestart, Service: "web"},
		home.OpenScriptMsg{Title: "script: deploy", Name: "deploy"},
		home.OpenAdHocMsg{Title: "$ uptime", Command: "uptime"},
	}
	for _, first := range requests {
		starts, stops := 0, 0
		start := func() (operations.Feed, error) {
			starts++
			return operations.Feed{Events: make(chan operations.Event), Stop: func() { stops++ }}, nil
		}
		model := appModel{
			home: home.New(home.Config{}, status.New(status.Config{})),
			backend: Backend{
				Logs:   func(string, int) (operations.Feed, error) { return start() },
				Action: func(operations.ServiceAction, string) (operations.Feed, error) { return start() },
				Script: func(string) (operations.Feed, error) { return start() },
				AdHoc:  func(string) (operations.Feed, error) { return start() },
			},
		}
		pending, command := model.Update(first)
		for _, next := range requests {
			var duplicate tea.Cmd
			pending, duplicate = pending.Update(next)
			if duplicate != nil {
				t.Fatalf("%T followed by %T scheduled another command while starting", first, next)
			}
		}
		opened, _ := pending.Update(command())
		for _, next := range requests {
			if _, duplicate := opened.Update(next); duplicate != nil {
				t.Fatalf("%T could replace an active follow", next)
			}
		}
		closed, _ := opened.Update(follow.CloseMsg{})
		if starts != 1 || stops != 1 {
			t.Fatalf("%T: started %d commands and stopped %d, want one each", first, starts, stops)
		}
		if _, command := closed.Update(first); command == nil {
			t.Fatalf("%T could not reopen after closing", first)
		}
	}
}

func TestAppDisposesSupersededFollowResult(t *testing.T) {
	var stops int
	model := appModel{
		home: home.New(home.Config{}, status.New(status.Config{})),
		backend: Backend{Logs: func(string, int) (operations.Feed, error) {
			return operations.Feed{Events: make(chan operations.Event), Stop: func() { stops++ }}, nil
		}},
	}
	request := home.OpenLogsMsg{Title: "logs: web", Service: "web"}
	pending, oldCommand := model.Update(request)
	closed, _ := pending.Update(tea.KeyMsg{Type: tea.KeyEsc})
	reopened, newCommand := closed.Update(request)
	newResult := newCommand().(feedMsg)
	current, _ := reopened.Update(newResult)
	current, command := current.Update(oldCommand())
	if stops != 1 || command != nil || current.(appModel).followView == nil {
		t.Fatal("obsolete response was not disposed while preserving the active follow")
	}
	current.Update(follow.CloseMsg{})
	if stops != 2 {
		t.Fatalf("active follow was not stopped on close: %d stops", stops)
	}
}

func TestAppCanStartAgainAfterFollowFailure(t *testing.T) {
	model := appModel{
		home: home.New(home.Config{}, status.New(status.Config{})),
		backend: Backend{Logs: func(string, int) (operations.Feed, error) {
			return operations.Feed{}, errors.New("connection failed")
		}},
	}
	request := home.OpenLogsMsg{Title: "logs: web", Service: "web"}
	pending, command := model.Update(request)
	failed, _ := pending.Update(command())
	if _, command := failed.Update(request); command == nil {
		t.Fatal("failed startup left further requests blocked")
	}
}

// A start in flight refuses every other request to open something, so it is
// said on the screen while it lasts, and esc gives up on it: on a link that
// stalled, the round trip it waits for does not come back.
func TestAPendingStartIsShownAndEscGivesUpOnIt(t *testing.T) {
	stops := 0
	model := newTestApp(Backend{Logs: func(string, int) (operations.Feed, error) {
		return operations.Feed{Events: make(chan operations.Event), Stop: func() { stops++ }}, nil
	}})
	request := home.OpenLogsMsg{Title: "logs: web", Service: "web"}
	pending, command := model.Update(request)
	if view := pending.View(); !strings.Contains(view, "opening logs: web") ||
		!strings.Contains(view, "esc cancel") {
		t.Errorf("a pending start is not on the screen:\n%s", view)
	}
	cancelled, _ := pending.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(cancelled.View(), "opening logs: web") {
		t.Errorf("the start was still announced after esc:\n%s", cancelled.View())
	}
	if _, again := cancelled.Update(request); again == nil {
		t.Error("esc left further requests refused")
	}
	// The abandoned call still answers, and its stream is stopped.
	if _, follow := cancelled.Update(command()); follow != nil || stops != 1 {
		t.Errorf("the abandoned start was opened (stops %d)", stops)
	}
}

// esc belongs to a menu or the prompt while one is open, pending start or not.
func TestEscClosesAMenuBeforeItCancelsAStart(t *testing.T) {
	model := newTestApp(Backend{Logs: func(string, int) (operations.Feed, error) {
		return closedFeed(), nil
	}})
	pending, _ := model.Update(home.OpenLogsMsg{Title: "logs: web", Service: "web"})
	prompt, _ := pending.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	closed, _ := prompt.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !closed.(appModel).feedStarting {
		t.Error("esc in the prompt cancelled the start instead of the prompt")
	}
}

// A CloseMsg with no view open — a second esc, delivered after the first had
// closed the view — closes nothing, and must not discard a start pending
// since.
func TestAStrayCloseDoesNotCancelAPendingStart(t *testing.T) {
	model := newTestApp(Backend{Logs: func(string, int) (operations.Feed, error) {
		return operations.Feed{Events: make(chan operations.Event), Stop: func() {}}, nil
	}})
	pending, command := model.Update(home.OpenLogsMsg{Title: "logs: web", Service: "web"})
	stray, _ := pending.Update(follow.CloseMsg{})
	opened, _ := stray.Update(command())
	if opened.(appModel).followView == nil {
		t.Error("a stray close discarded the feed that was starting")
	}
}

func newTestApp(backend Backend) appModel {
	return appModel{backend: backend,
		home: home.New(home.Config{}, status.New(status.Config{}))}
}

func closedFeed() operations.Feed {
	events := make(chan operations.Event)
	close(events)
	return operations.Feed{Events: events, Stop: func() {}}
}

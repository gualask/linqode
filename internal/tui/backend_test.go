package tui

import (
	"errors"
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
	model := appModel{backend: backend}

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
	model := appModel{backend: backend}

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
	model := appModel{backend: backend}

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
	model := appModel{backend: backend}

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
	closed, _ := pending.Update(follow.CloseMsg{})
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

func closedFeed() operations.Feed {
	events := make(chan operations.Event)
	close(events)
	return operations.Feed{Events: events, Stop: func() {}}
}

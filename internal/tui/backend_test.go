package tui

import (
	"testing"

	"github.com/gualask/linqode/internal/operations"
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

	_, command := model.Update(openLogsMsg{title: "logs: web", service: "web"})
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

	_, command := model.Update(openActionMsg{
		title: "restart: web", action: operations.ActionRestart, service: "web",
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

	_, command := model.Update(openScriptMsg{title: "script: deploy", name: "deploy"})
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

	_, command := model.Update(openAdHocMsg{title: "$ uptime", command: "uptime"})
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

	_, command := model.Update(openStatsMsg{})
	message, ok := command().(statsFeedMsg)
	if !ok || message.err != nil {
		t.Fatalf("message = %#v", command())
	}
	if !called {
		t.Fatal("LiveStats was not called")
	}
}

func closedFeed() operations.Feed {
	events := make(chan operations.Event)
	close(events)
	return operations.Feed{Events: events, Stop: func() {}}
}

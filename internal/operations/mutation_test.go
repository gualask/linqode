package operations

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/gualask/linqode/internal/remote"
)

func TestActionValidatesServiceAndRunsExactCommandOnce(t *testing.T) {
	tests := []struct {
		action ServiceAction
		verb   string
	}{
		{action: ActionRestart, verb: "restart"},
		{action: ActionStop, verb: "stop"},
		{action: ActionStart, verb: "start"},
	}
	for _, test := range tests {
		t.Run(test.verb, func(t *testing.T) {
			remoteEvents := make(chan remote.ExecEvent, 1)
			remoteEvents <- remote.ExecEvent{Kind: remote.ExecExit, ExitCode: 0}
			close(remoteEvents)
			executor := &fakeExecutor{
				results: []execResult{{output: remote.ExecOutput{Stdout: []byte(serviceJSON), ExitCode: 0}}},
				stream: func(context.Context, string) (<-chan remote.ExecEvent, error) {
					return remoteEvents, nil
				},
			}
			operator := NewHostOperator(executor, "/srv/app")

			feed, err := operator.Action(context.Background(), test.action, "web")
			if err != nil {
				t.Fatal(err)
			}
			_ = collect(feed)
			if !slices.Equal(executor.commands, []string{
				"cd '/srv/app' && docker compose ps --all --format json",
			}) {
				t.Fatalf("validation commands = %q", executor.commands)
			}
			want := "cd '/srv/app' && docker compose " + test.verb + " 'web'"
			if !slices.Equal(executor.streamCommands, []string{want}) {
				t.Fatalf("stream commands = %q, want %q", executor.streamCommands, want)
			}
		})
	}
}

func TestActionRejectsUnknownServiceAndInvalidIntentBeforeStreaming(t *testing.T) {
	executor := &fakeExecutor{results: []execResult{{
		output: remote.ExecOutput{Stdout: []byte(serviceJSON), ExitCode: 0},
	}}}
	operator := NewHostOperator(executor, "")
	_, err := operator.Action(context.Background(), ActionRestart, "db")
	var unknown UnknownServiceError
	if !errors.As(err, &unknown) || unknown.Name != "db" {
		t.Fatalf("Action() error = %v", err)
	}
	if len(executor.streamCommands) != 0 {
		t.Fatalf("unknown service started %q", executor.streamCommands)
	}

	executor = &fakeExecutor{}
	_, err = NewHostOperator(executor, "").Action(context.Background(), ServiceAction(99), "web")
	var invalid InvalidActionError
	if !errors.As(err, &invalid) || invalid.Action != 99 {
		t.Fatalf("invalid action error = %v", err)
	}
	if len(executor.commands) != 0 || len(executor.streamCommands) != 0 {
		t.Fatalf("invalid action executed commands: %+v", executor)
	}
}

func TestScriptResolvesConfiguredNameAndPassesCommandVerbatim(t *testing.T) {
	remoteEvents := make(chan remote.ExecEvent, 1)
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecExit, ExitCode: 0}
	close(remoteEvents)
	executor := &fakeExecutor{stream: func(context.Context, string) (<-chan remote.ExecEvent, error) {
		return remoteEvents, nil
	}}
	operator := NewHostOperator(executor, "/srv/app",
		Script{Name: "deploy", Command: "deploy.sh --yes"},
		Script{Name: "backup", Command: "/opt/bin/backup"},
	)
	feed, err := operator.Script(context.Background(), "deploy")
	if err != nil {
		t.Fatal(err)
	}
	_ = collect(feed)
	if len(executor.commands) != 0 {
		t.Fatalf("script performed unexpected validation commands: %q", executor.commands)
	}
	if !slices.Equal(executor.streamCommands, []string{"deploy.sh --yes"}) {
		t.Fatalf("script command = %q", executor.streamCommands)
	}
}

func TestScriptRejectsUnknownNameWithoutExecuting(t *testing.T) {
	executor := &fakeExecutor{}
	_, err := NewHostOperator(executor, "", Script{Name: "deploy", Command: "deploy.sh"}).
		Script(context.Background(), "backup")
	var unknown UnknownScriptError
	if !errors.As(err, &unknown) || unknown.Name != "backup" {
		t.Fatalf("Script() error = %v", err)
	}
	if len(executor.commands) != 0 || len(executor.streamCommands) != 0 {
		t.Fatalf("unknown script executed commands: %+v", executor)
	}
}

func TestMutationsNeverRetryAStreamStart(t *testing.T) {
	startErr := errors.New("uncertain remote start")
	tests := []struct {
		name     string
		executor *fakeExecutor
		start    func(*HostOperator) error
	}{
		{
			name: "action",
			executor: &fakeExecutor{results: []execResult{{
				output: remote.ExecOutput{Stdout: []byte(serviceJSON), ExitCode: 0},
			}}},
			start: func(operator *HostOperator) error {
				_, err := operator.Action(context.Background(), ActionRestart, "web")
				return err
			},
		},
		{
			name:     "script",
			executor: &fakeExecutor{},
			start: func(operator *HostOperator) error {
				_, err := operator.Script(context.Background(), "deploy")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			test.executor.stream = func(context.Context, string) (<-chan remote.ExecEvent, error) {
				calls++
				return nil, startErr
			}
			operator := NewHostOperator(test.executor, "", Script{Name: "deploy", Command: "deploy.sh"})
			if err := test.start(operator); !errors.Is(err, startErr) {
				t.Fatalf("start error = %v", err)
			}
			if calls != 1 {
				t.Fatalf("ExecStream called %d times", calls)
			}
		})
	}
}

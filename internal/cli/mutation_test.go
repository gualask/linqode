package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gualask/linqode/internal/operations"
)

type fakeMutator struct {
	action func(context.Context, operations.ServiceAction, string) (operations.Feed, error)
	script func(context.Context, string) (operations.Feed, error)
}

func (f fakeMutator) Action(
	ctx context.Context,
	action operations.ServiceAction,
	service string,
) (operations.Feed, error) {
	return f.action(ctx, action, service)
}

func (f fakeMutator) Script(ctx context.Context, name string) (operations.Feed, error) {
	return f.script(ctx, name)
}

func TestRunMutationMapsLifecycleCommandsToTypedActions(t *testing.T) {
	tests := []struct {
		command Command
		action  operations.ServiceAction
	}{
		{command: CommandRestart, action: operations.ActionRestart},
		{command: CommandStop, action: operations.ActionStop},
		{command: CommandStart, action: operations.ActionStart},
	}
	for _, test := range tests {
		t.Run(string(test.command), func(t *testing.T) {
			mutator := fakeMutator{action: func(
				_ context.Context,
				action operations.ServiceAction,
				service string,
			) (operations.Feed, error) {
				if action != test.action || service != "web" {
					t.Fatalf("Action(%d, %q)", action, service)
				}
				return eventFeed(nil, operations.Event{Kind: operations.EventExit, ExitCode: 0}), nil
			}}
			var stdout, stderr bytes.Buffer
			code := RunMutation(context.Background(), Invocation{
				Command: test.command, Host: "production", Service: "web",
			}, mutator, &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("code = %d, stderr = %q", code, stderr.String())
			}
			events := decodeJSONLines(t, stdout.String())
			if len(events) != 1 || events[0]["type"] != "exit" || events[0]["code"] != float64(0) {
				t.Fatalf("events = %#v", events)
			}
		})
	}
}

func TestRunMutationStreamsScriptOutputAndPropagatesRemoteExit(t *testing.T) {
	mutator := fakeMutator{script: func(_ context.Context, name string) (operations.Feed, error) {
		if name != "deploy" {
			t.Fatalf("Script(%q)", name)
		}
		return eventFeed(nil,
			operations.Event{Kind: operations.EventStdout, Text: "deploying"},
			operations.Event{Kind: operations.EventStderr, Text: "warning"},
			operations.Event{Kind: operations.EventExit, ExitCode: 7},
		), nil
	}}

	var stdout, stderr bytes.Buffer
	code := RunMutation(context.Background(), Invocation{
		Command: CommandScript, Host: "production", ScriptName: "deploy",
	}, mutator, &stdout, &stderr)
	if code != 7 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	events := decodeJSONLines(t, stdout.String())
	if len(events) != 3 || events[0]["type"] != "stdout" || events[0]["data"] != "deploying" ||
		events[1]["type"] != "stderr" || events[1]["data"] != "warning" ||
		events[2]["type"] != "exit" || events[2]["code"] != float64(7) {
		t.Fatalf("events = %#v", events)
	}
}

// SSH carries a 32-bit exit status and a process exit code is a byte: 256
// handed to os.Exit would read as success. A status the process cannot carry
// exits 255, the exit event keeps the exact value.
func TestRunMutationClampsARemoteExitTheProcessCannotCarry(t *testing.T) {
	for _, remote := range []int{256, 512, 70000} {
		mutator := fakeMutator{script: func(context.Context, string) (operations.Feed, error) {
			return eventFeed(nil, operations.Event{Kind: operations.EventExit, ExitCode: remote}), nil
		}}
		var stdout, stderr bytes.Buffer
		code := RunMutation(context.Background(), Invocation{
			Command: CommandScript, Host: "production", ScriptName: "deploy",
		}, mutator, &stdout, &stderr)
		if code != 255 || stderr.Len() != 0 {
			t.Errorf("remote %d: code = %d, stderr = %q", remote, code, stderr.String())
		}
		events := decodeJSONLines(t, stdout.String())
		if len(events) != 1 || events[0]["code"] != float64(remote) {
			t.Errorf("remote %d: events = %#v", remote, events)
		}
	}
}

func TestRunMutationClassifiesValidationAndIncompleteStreams(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		mutator  fakeMutator
		invoke   Invocation
		wantCode int
		wantKind string
	}{
		{
			name: "unknown service", ctx: context.Background(),
			invoke: Invocation{Command: CommandRestart, Service: "missing"},
			mutator: fakeMutator{action: func(context.Context, operations.ServiceAction, string) (operations.Feed, error) {
				return operations.Feed{}, operations.UnknownServiceError{Name: "missing"}
			}},
			wantCode: 2, wantKind: "unknown_service",
		},
		{
			name: "unknown script", ctx: context.Background(),
			invoke: Invocation{Command: CommandScript, ScriptName: "missing"},
			mutator: fakeMutator{script: func(context.Context, string) (operations.Feed, error) {
				return operations.Feed{}, operations.UnknownScriptError{Name: "missing"}
			}},
			wantCode: 2, wantKind: "unknown_script",
		},
		{
			name: "missing exit", ctx: context.Background(),
			invoke: Invocation{Command: CommandScript, ScriptName: "deploy"},
			mutator: fakeMutator{script: func(context.Context, string) (operations.Feed, error) {
				return eventFeed(nil, operations.Event{Kind: operations.EventStdout, Text: "partial"}), nil
			}},
			wantCode: 1, wantKind: "remote_exit_missing",
		},
		{
			name: "cancelled", ctx: cancelledContext(),
			invoke: Invocation{Command: CommandScript, ScriptName: "deploy"},
			mutator: fakeMutator{script: func(context.Context, string) (operations.Feed, error) {
				return eventFeed(nil), nil
			}},
			wantCode: 130, wantKind: "transport_failed",
		},
		{
			name: "stream start failed", ctx: context.Background(),
			invoke: Invocation{Command: CommandScript, ScriptName: "deploy"},
			mutator: fakeMutator{script: func(context.Context, string) (operations.Feed, error) {
				return operations.Feed{}, errors.New("connection lost")
			}},
			wantCode: 1, wantKind: "transport_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunMutation(test.ctx, test.invoke, test.mutator, &stdout, &stderr)
			if code != test.wantCode || decodeErrorKind(t, stderr.Bytes()) != test.wantKind {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

var _ Mutator = fakeMutator{}

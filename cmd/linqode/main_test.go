package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/compose"
	"strings"

	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/probe"
)

func TestRunMachineRejectsUnknownAndDisabledHostsBeforeConnecting(t *testing.T) {
	disabled := false
	catalog := operations.NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"restricted": {
			Host: "deploy@example.com", HostMetrics: &disabled,
			Scripts: map[string]string{"deploy": "deploy.sh"},
		},
	}})
	connectCalls := 0
	connector := func(context.Context, operations.ConfiguredHost) (cli.SafeOperator, func(), error) {
		connectCalls++
		return nil, nil, errors.New("unexpected connection")
	}

	tests := []struct {
		name     string
		invoke   cli.Invocation
		wantCode int
		wantKind string
	}{
		{
			name:     "inline target is not configured",
			invoke:   cli.Invocation{Command: cli.CommandStatus, Host: "deploy@example.com"},
			wantCode: 2, wantKind: "unknown_host",
		},
		{
			name:     "stats disabled by host",
			invoke:   cli.Invocation{Command: cli.CommandStats, Host: "restricted"},
			wantCode: 1, wantKind: "unsupported_operation",
		},
		{
			name:     "script is not configured",
			invoke:   cli.Invocation{Command: cli.CommandScript, Host: "restricted", ScriptName: "missing"},
			wantCode: 2, wantKind: "unknown_script",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runMachine(context.Background(), test.invoke, catalog, connector, &stdout, &stderr)
			if code != test.wantCode || stdout.Len() != 0 {
				t.Fatalf("code = %d, stdout = %q", code, stdout.String())
			}
			assertErrorKind(t, stderr.Bytes(), test.wantKind)
		})
	}
	if connectCalls != 0 {
		t.Fatalf("connector called %d times", connectCalls)
	}
}

func TestRunMachineConnectsConfiguredMutationAndCloses(t *testing.T) {
	catalog := operations.NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"production": {
			Host:    "production-alias",
			Scripts: map[string]string{"deploy": "deploy.sh"},
		},
	}})
	closed := false
	connector := func(_ context.Context, configured operations.ConfiguredHost) (cli.SafeOperator, func(), error) {
		if configured.Name != "production" {
			t.Fatalf("configured = %+v", configured)
		}
		return machineMutationOperator{script: func(_ context.Context, name string) (operations.Feed, error) {
			if name != "deploy" {
				t.Fatalf("Script(%q)", name)
			}
			return machineEventFeed(
				operations.Event{Kind: operations.EventStdout, Text: "done"},
				operations.Event{Kind: operations.EventExit, ExitCode: 0},
			), nil
		}}, func() { closed = true }, nil
	}

	var stdout, stderr bytes.Buffer
	code := runMachine(context.Background(), cli.Invocation{
		Command: cli.CommandScript, Host: "production", ScriptName: "deploy",
	}, catalog, connector, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !closed {
		t.Fatalf("code = %d, closed = %v, stderr = %q", code, closed, stderr.String())
	}
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 2 || events[0]["type"] != "stdout" || events[1]["type"] != "exit" {
		t.Fatalf("events = %#v", events)
	}
}

func TestRunMachineConnectsConfiguredReadAndCloses(t *testing.T) {
	catalog := operations.NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"production": {Host: "production-alias", ComposeDir: "/srv/app"},
	}})
	closed := false
	connector := func(_ context.Context, configured operations.ConfiguredHost) (cli.SafeOperator, func(), error) {
		if configured.Name != "production" || configured.Spec != "production-alias" ||
			configured.ComposeDir != "/srv/app" {
			t.Fatalf("configured = %+v", configured)
		}
		return machineObserver{}, func() { closed = true }, nil
	}

	var stdout, stderr bytes.Buffer
	code := runMachine(context.Background(), cli.Invocation{
		Command: cli.CommandStatus, Host: "production",
	}, catalog, connector, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !closed {
		t.Fatalf("code = %d, closed = %v, stderr = %q", code, closed, stderr.String())
	}
	var document struct {
		Host     string `json:"host"`
		Services []any  `json:"services"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Host != "production" || document.Services == nil {
		t.Fatalf("document = %+v", document)
	}
}

type machineObserver struct {
	// probe is what the host answered, zero when the test does not care —
	// which is the reading that establishes nothing and rejects nothing.
	probe probe.Result
}

func (m machineObserver) Probe(context.Context) (probe.Result, error) {
	return m.probe, nil
}

type machineMutationOperator struct {
	machineObserver
	script func(context.Context, string) (operations.Feed, error)
}

func (m machineMutationOperator) Script(ctx context.Context, name string) (operations.Feed, error) {
	return m.script(ctx, name)
}

func (machineObserver) Status(context.Context) ([]compose.Service, error) {
	return []compose.Service{}, nil
}

func (machineObserver) HostMetrics(context.Context) (host.Metrics, error) {
	return host.Metrics{}, errors.New("unexpected HostMetrics")
}

func (machineObserver) ContainerStats(context.Context) ([]compose.ContainerStats, error) {
	return nil, errors.New("unexpected ContainerStats")
}

func (machineObserver) Logs(context.Context, string, int, bool) (operations.Feed, error) {
	return operations.Feed{}, errors.New("unexpected Logs")
}

func (machineObserver) FollowStats(context.Context) (operations.Feed, error) {
	return operations.Feed{}, errors.New("unexpected FollowStats")
}

func (machineObserver) Action(context.Context, operations.ServiceAction, string) (operations.Feed, error) {
	return operations.Feed{}, errors.New("unexpected Action")
}

func (machineObserver) Script(context.Context, string) (operations.Feed, error) {
	return operations.Feed{}, errors.New("unexpected Script")
}

func machineEventFeed(events ...operations.Event) operations.Feed {
	stream := make(chan operations.Event, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return operations.Feed{Events: stream, Stop: func() {}}
}

func writeDefaultConfig(t *testing.T, contents string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "linqode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertErrorKind(t *testing.T, raw []byte, want string) {
	t.Helper()
	var document struct {
		Error struct {
			Kind string `json:"kind"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
	if document.Error.Kind != want {
		t.Fatalf("error kind = %q, want %q", document.Error.Kind, want)
	}
}

// A machine command on a host whose socket refuses this user must say so in
// the envelope, not exit non-zero with whatever the shell printed.
func TestRunMachineReportsWhatTheHostCannotDo(t *testing.T) {
	catalog := operations.NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"production": {Host: "production-alias", ComposeDir: "/srv/app"},
	}})
	denied := probe.Parse([]byte(
		"#docker\n/usr/bin/docker\n#daemon\npermission denied\n#compose\n5.3.1\n#dir\npresent\n"),
		"/srv/app")
	connector := func(context.Context, operations.ConfiguredHost) (cli.SafeOperator, func(), error) {
		return machineObserver{probe: denied}, func() {}, nil
	}

	var stdout, stderr bytes.Buffer
	code := runMachine(context.Background(), cli.Invocation{
		Command: cli.CommandStatus, Host: "production",
	}, catalog, connector, &stdout, &stderr)
	if code == 0 || stdout.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q", code, stdout.String())
	}
	var document struct {
		Error struct {
			Kind      string `json:"kind"`
			Message   string `json:"message"`
			Operation string `json:"operation"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Error.Kind != "docker_permission_denied" {
		t.Errorf("kind = %q, want docker_permission_denied", document.Error.Kind)
	}
	if !strings.Contains(document.Error.Message, "docker") ||
		document.Error.Operation != "status" {
		t.Errorf("error = %+v", document.Error)
	}
}

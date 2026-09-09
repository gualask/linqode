package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExecuteLocalDiscoveryDoesNotDial(t *testing.T) {
	writeDefaultConfig(t, `
[hosts.production]
host = "deploy@203.0.113.10:1"
[hosts.production.scripts]
deploy = "deploy.sh --yes"
`)
	for _, args := range [][]string{{"hosts"}, {"scripts", "production"}} {
		var stdout, stderr bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		code := execute(ctx, args, &stdout, &stderr)
		cancel()
		if code != 0 {
			t.Fatalf("execute(%v) code = %d, stderr = %s", args, code, stderr.String())
		}
		var document map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
			t.Fatalf("execute(%v) emitted invalid JSON: %v", args, err)
		}
	}
}

func TestExecuteMachineCommandRejectsConfigOverride(t *testing.T) {
	assertExecuteFailure(t, []string{"--config", "other.toml", "hosts"}, 2, "invalid_option")
}

func TestExecuteRejectsScriptRuntimeArgumentsBeforeLoadingConfig(t *testing.T) {
	assertExecuteFailure(t, []string{"script", "production", "deploy", "--force"}, 2, "usage")
}

func TestExecuteReportsDefaultConfigFailureAsJSON(t *testing.T) {
	writeDefaultConfig(t, "not = [valid")
	assertExecuteFailure(t, []string{"hosts"}, 1, "config_error")
}

func TestExecuteRemoteReadValidationHappensBeforeDial(t *testing.T) {
	writeDefaultConfig(t, `
[hosts.restricted]
host = "deploy@203.0.113.10:1"
host_metrics = false
`)
	tests := []struct {
		args     []string
		wantCode int
		wantKind string
	}{
		{args: []string{"status", "deploy@203.0.113.10:1"}, wantCode: 2, wantKind: "unknown_host"},
		{args: []string{"stats", "restricted"}, wantCode: 1, wantKind: "unsupported_operation"},
	}
	for _, test := range tests {
		assertExecuteFailure(t, test.args, test.wantCode, test.wantKind)
	}
}

func assertExecuteFailure(t *testing.T, args []string, wantCode int, wantKind string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), args, &stdout, &stderr)
	if code != wantCode || stdout.Len() != 0 {
		t.Fatalf("execute(%v) code = %d, stdout = %q", args, code, stdout.String())
	}
	assertErrorKind(t, stderr.Bytes(), wantKind)
}

// The machine boundary's value is the gap between what an agent can do
// without Linqode and what Linqode grants it. On this machine that gap is
// zero, so `local` is a host only the operator may reach — and `hosts`, the
// machine surface's own discovery, must not list what every other command
// there refuses.
func TestExecuteKeepsLocalHostsOffTheMachineInterface(t *testing.T) {
	writeDefaultConfig(t, `
[hosts.laptop]
host = "local"
[hosts.laptop.scripts]
build = "make"

[hosts.production]
host = "deploy@203.0.113.10:1"
`)
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if code := execute(ctx, []string{"hosts"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hosts code = %d, stderr = %s", code, stderr.String())
	}
	if listed := stdout.String(); strings.Contains(listed, "laptop") {
		t.Errorf("hosts listed a local host: %s", listed)
	} else if !strings.Contains(listed, "production") {
		t.Errorf("hosts dropped the remote host too: %s", listed)
	}

	for _, args := range [][]string{
		{"scripts", "laptop"},
		{"status", "laptop"},
		{"script", "laptop", "build"},
	} {
		assertExecuteFailure(t, args, 2, "local_host")
	}
}

// A local session has nothing to resolve and nobody to announce it to.
func TestOpenTransportRunsLocallyWithoutConnecting(t *testing.T) {
	var stderr bytes.Buffer
	link, err := openTransport(context.Background(), "local", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer link.close()

	if stderr.Len() != 0 {
		t.Errorf("announced a local session: %q", stderr.String())
	}
	if link.target != "local" {
		t.Errorf("target is %q, want local", link.target)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := link.executor.Exec(ctx, "echo reached")
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Stdout) != "reached\n" || out.ExitCode != 0 {
		t.Errorf("got %+v", out)
	}
}

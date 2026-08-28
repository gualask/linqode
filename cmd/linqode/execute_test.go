package main

import (
	"bytes"
	"context"
	"encoding/json"
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

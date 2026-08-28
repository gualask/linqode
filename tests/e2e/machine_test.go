//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineBinaryCommands(t *testing.T) {
	binary, home := prepareMachineBinary(t)
	t.Run("status", func(t *testing.T) { assertMachineStatus(t, binary, home) })
	t.Run("bounded logs", func(t *testing.T) { assertMachineLogs(t, binary, home) })
	t.Run("restart", func(t *testing.T) { assertMachineRestart(t, binary, home) })
	t.Run("configured script", func(t *testing.T) { assertMachineScript(t, binary, home) })
	t.Run("configured script propagates remote exit", func(t *testing.T) {
		assertMachineScriptExit(t, binary, home)
	})
}

func assertMachineStatus(t *testing.T, binary, home string) {
	t.Helper()
	stdout, stderr := invokeMachineBinary(t, binary, home, "status", "fixture")
	var document struct {
		SchemaVersion int    `json:"schema_version"`
		Host          string `json:"host"`
		Services      []struct {
			Service string `json:"service"`
		} `json:"services"`
	}
	if err := json.Unmarshal(stdout, &document); err != nil {
		t.Fatalf("invalid status JSON %q: %v\nstderr: %s", stdout, err, stderr)
	}
	if document.SchemaVersion != 1 || document.Host != "fixture" || len(document.Services) < demoServices {
		t.Fatalf("status document = %+v", document)
	}
}

func assertMachineLogs(t *testing.T, binary, home string) {
	t.Helper()
	stdout, stderr := invokeMachineBinary(t, binary, home,
		"logs", "--tail", "3", "fixture", "web")
	events := decodeMachineEvents(t, stdout, stderr)
	if len(events) < 2 || events[0]["type"] != "log" {
		t.Fatalf("log events = %#v", events)
	}
	last := events[len(events)-1]
	if last["type"] != "exit" || last["code"] != float64(0) {
		t.Fatalf("terminal event = %#v", last)
	}
}

func assertMachineRestart(t *testing.T, binary, home string) {
	t.Helper()
	stdout, stderr := invokeMachineBinary(t, binary, home,
		"restart", "fixture", "web")
	events := decodeMachineEvents(t, stdout, stderr)
	last := events[len(events)-1]
	if last["type"] != "exit" || last["code"] != float64(0) {
		t.Fatalf("restart events = %#v", events)
	}
}

func assertMachineScript(t *testing.T, binary, home string) {
	t.Helper()
	stdout, stderr := invokeMachineBinary(t, binary, home,
		"script", "fixture", "probe")
	events := decodeMachineEvents(t, stdout, stderr)
	if len(events) != 2 || events[0]["type"] != "stdout" || events[0]["data"] != "script-ok" ||
		events[1]["type"] != "exit" || events[1]["code"] != float64(0) {
		t.Fatalf("script events = %#v", events)
	}
}

func assertMachineScriptExit(t *testing.T, binary, home string) {
	t.Helper()
	stdout, stderr, err := invokeMachineBinaryResult(t, binary, home,
		"script", "fixture", "fail")
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 7 || len(stderr) != 0 {
		t.Fatalf("error = %v, stderr = %q", err, stderr)
	}
	events := decodeMachineEvents(t, stdout, stderr)
	if len(events) != 2 || events[0]["type"] != "stderr" || events[0]["data"] != "script-failed" ||
		events[1]["type"] != "exit" || events[1]["code"] != float64(7) {
		t.Fatalf("failure events = %#v", events)
	}
}

func prepareMachineBinary(t *testing.T) (binary, home string) {
	t.Helper()
	dir := t.TempDir()
	binary = filepath.Join(dir, "linqode")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/linqode")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building linqode: %v", err)
	}

	home = filepath.Join(dir, "home")
	configDir := filepath.Join(home, ".config", "linqode")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`
[hosts.fixture]
host = "fixture"
compose_dir = %q

[hosts.fixture.scripts]
probe = "printf script-ok"
fail = "printf script-failed >&2; exit 7"
`, composeDir)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	sshConfig := fmt.Sprintf(`Host fixture
  HostName %s
  User %s
  Port %d
  IdentityFile %s
`, fixtureHost, fixtureUser, fixturePort, identityFile)
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(sshConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	knownHosts, err := os.ReadFile(runKnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), knownHosts, 0o600); err != nil {
		t.Fatal(err)
	}
	return binary, home
}

func invokeMachineBinary(t *testing.T, binary, home string, args ...string) ([]byte, []byte) {
	t.Helper()
	stdout, stderr, err := invokeMachineBinaryResult(t, binary, home, args...)
	if err != nil {
		t.Fatalf("linqode %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout, stderr
}

func invokeMachineBinaryResult(t *testing.T, binary, home string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*guardTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(os.Environ(), "HOME="+home, "SSH_AUTH_SOCK=")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func decodeMachineEvents(t *testing.T, stdout, stderr []byte) []map[string]any {
	t.Helper()
	var events []map[string]any
	for line := range strings.Lines(string(stdout)) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL line %q: %v\nstderr: %s", line, err, stderr)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Fatalf("no JSONL events; stderr: %s", stderr)
	}
	return events
}

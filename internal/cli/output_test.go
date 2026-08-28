package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/operations"
)

func TestRunHostsAndScripts(t *testing.T) {
	catalog := operations.NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"production": {
			Host: "deploy@203.0.113.10",
			Scripts: map[string]string{
				"deploy": "deploy.sh --yes",
				"backup": "backup.sh",
			},
		},
	}})

	tests := []struct {
		name       string
		invocation Invocation
		assert     func(*testing.T, map[string]any)
	}{
		{
			name:       "hosts",
			invocation: Invocation{Command: CommandHosts},
			assert: func(t *testing.T, document map[string]any) {
				hosts := document["hosts"].([]any)
				if len(hosts) != 1 || hosts[0].(map[string]any)["name"] != "production" {
					t.Fatalf("hosts = %#v", hosts)
				}
			},
		},
		{
			name:       "scripts",
			invocation: Invocation{Command: CommandScripts, Host: "production"},
			assert: func(t *testing.T, document map[string]any) {
				scripts := document["scripts"].([]any)
				if len(scripts) != 2 || scripts[0].(map[string]any)["name"] != "backup" || scripts[1].(map[string]any)["name"] != "deploy" {
					t.Fatalf("scripts = %#v", scripts)
				}
				if bytes.Contains(mustJSON(t, document), []byte("deploy.sh")) {
					t.Fatal("script command body leaked into discovery output")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(test.invocation, catalog, &stdout, &stderr); code != 0 {
				t.Fatalf("Run() code = %d, stderr = %s", code, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %s", stderr.String())
			}
			var document map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if document["schema_version"] != float64(1) {
				t.Fatalf("schema_version = %#v", document["schema_version"])
			}
			test.assert(t, document)
		})
	}
}

func TestRunUsesEmptyJSONArrays(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(Invocation{Command: CommandHosts}, operations.NewCatalog(&config.Config{}), &stdout, &stderr)
	if code != 0 || stdout.String() != "{\"schema_version\":1,\"hosts\":[]}\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestRunReportsUnknownHostAsInputError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(Invocation{Command: CommandScripts, Host: "missing"},
		operations.NewCatalog(&config.Config{}), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q", code, stdout.String())
	}

	var document struct {
		SchemaVersion int `json:"schema_version"`
		Error         struct {
			Kind      string `json:"kind"`
			Operation string `json:"operation"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &document); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
	if document.SchemaVersion != 1 || document.Error.Kind != "unknown_host" || document.Error.Operation != "scripts" {
		t.Fatalf("error = %+v", document)
	}
}

func TestRunRejectsNonLocalCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(Invocation{Command: CommandStatus}, operations.NewCatalog(&config.Config{}), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q", code, stdout.String())
	}
	var document map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &document); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

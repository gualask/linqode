package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
)

type fakeObserver struct {
	status         func(context.Context) ([]compose.Service, error)
	hostMetrics    func(context.Context) (host.Metrics, error)
	containerStats func(context.Context) ([]compose.ContainerStats, error)
	logs           func(context.Context, string, int, bool) (operations.Feed, error)
	followStats    func(context.Context) (operations.Feed, error)
}

func (f fakeObserver) Status(ctx context.Context) ([]compose.Service, error) {
	return f.status(ctx)
}

func (f fakeObserver) HostMetrics(ctx context.Context) (host.Metrics, error) {
	return f.hostMetrics(ctx)
}

func (f fakeObserver) ContainerStats(ctx context.Context) ([]compose.ContainerStats, error) {
	return f.containerStats(ctx)
}

func (f fakeObserver) Logs(ctx context.Context, service string, tail int, follow bool) (operations.Feed, error) {
	return f.logs(ctx, service, tail, follow)
}

func (f fakeObserver) FollowStats(ctx context.Context) (operations.Feed, error) {
	return f.followStats(ctx)
}

func TestRunReadStatusWritesOneJSONDocument(t *testing.T) {
	restarts := 2
	observer := fakeObserver{status: func(context.Context) ([]compose.Service, error) {
		return []compose.Service{{
			Name: "app-web-1", Service: "web", State: "running", Health: "healthy",
			Restarts: &restarts, Status: "Up", Publishers: []compose.Publisher{{
				PublishedPort: 8080, TargetPort: 80, Protocol: "tcp",
			}},
		}}, nil
	}}

	var stdout, stderr bytes.Buffer
	code := RunRead(context.Background(), Invocation{Command: CommandStatus, Host: "production"},
		observer, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	document := decodeJSON(t, stdout.Bytes())
	if document["schema_version"] != float64(1) || document["host"] != "production" {
		t.Fatalf("document = %#v", document)
	}
	service := document["services"].([]any)[0].(map[string]any)
	if service["service"] != "web" || service["container"] != "app-web-1" ||
		service["restarts"] != float64(2) || service["ports"] != "8080->80/tcp" {
		t.Fatalf("service = %#v", service)
	}
}

func TestRunReadStatsWritesRawMachineValues(t *testing.T) {
	observer := fakeObserver{
		hostMetrics: func(context.Context) (host.Metrics, error) {
			return host.Metrics{
				Load1: 1.25, CPUs: 4, Uptime: 90 * time.Second,
				MemTotalKB: 100, MemAvailableKB: 25, DiskTotalKB: 200, DiskUsedKB: 50,
			}, nil
		},
		containerStats: func(context.Context) ([]compose.ContainerStats, error) {
			return []compose.ContainerStats{{Name: "app-web-1", CPUPerc: "3.20%", MemUsage: "4MiB / 1GiB"}}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := RunRead(context.Background(), Invocation{Command: CommandStats, Host: "production"},
		observer, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	document := decodeJSON(t, stdout.Bytes())
	metrics := document["host_metrics"].(map[string]any)
	if metrics["memory_total_bytes"] != float64(102400) ||
		metrics["memory_used_bytes"] != float64(76800) || metrics["uptime_seconds"] != float64(90) {
		t.Fatalf("host metrics = %#v", metrics)
	}
	container := document["containers"].([]any)[0].(map[string]any)
	if container["container"] != "app-web-1" || container["cpu_percent"] != "3.20%" {
		t.Fatalf("container = %#v", container)
	}
}

func TestRunReadFollowStatsWritesJSONLines(t *testing.T) {
	stopped := false
	observer := fakeObserver{
		hostMetrics: func(context.Context) (host.Metrics, error) {
			return host.Metrics{Load1: 0.5, CPUs: 2}, nil
		},
		followStats: func(context.Context) (operations.Feed, error) {
			return eventFeed(&stopped,
				operations.Event{Kind: operations.EventStats, Stats: compose.ContainerStats{Name: "app-web-1", CPUPerc: "1.00%"}},
				operations.Event{Kind: operations.EventStderr, Text: "daemon warning"},
				operations.Event{Kind: operations.EventExit, ExitCode: 0},
			), nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := RunRead(context.Background(), Invocation{Command: CommandStats, Host: "production", Follow: true},
		observer, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !stopped {
		t.Fatalf("code = %d, stopped = %v, stderr = %q", code, stopped, stderr.String())
	}
	documents := decodeJSONLines(t, stdout.String())
	wantTypes := []string{"host_stats", "container_stats", "stderr", "exit"}
	for i, want := range wantTypes {
		if documents[i]["type"] != want {
			t.Fatalf("event %d = %#v", i, documents[i])
		}
	}
}

func TestRunReadLogsPreservesRawAndStructuredLines(t *testing.T) {
	observer := fakeObserver{logs: func(_ context.Context, service string, tail int, follow bool) (operations.Feed, error) {
		if service != "web" || tail != 25 || !follow {
			t.Fatalf("Logs(%q, %d, %v)", service, tail, follow)
		}
		return eventFeed(nil,
			operations.Event{Kind: operations.EventLog, Text: `{"level":"info","http":{"status":200}}`},
			operations.Event{Kind: operations.EventLog, Text: ""},
			operations.Event{Kind: operations.EventStderr, Text: "warning"},
			operations.Event{Kind: operations.EventExit, ExitCode: 0},
		), nil
	}}

	var stdout, stderr bytes.Buffer
	code := RunRead(context.Background(), Invocation{
		Command: CommandLogs, Host: "production", Service: "web", Tail: 25, Follow: true,
	}, observer, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	documents := decodeJSONLines(t, stdout.String())
	if documents[0]["line"] == nil || documents[0]["record"].(map[string]any)["http.status"] != "200" {
		t.Fatalf("structured log = %#v", documents[0])
	}
	if line, ok := documents[1]["line"]; !ok || line != "" {
		t.Fatalf("empty log line = %#v", documents[1])
	}
	if documents[2]["type"] != "stderr" || documents[3]["code"] != float64(0) {
		t.Fatalf("events = %#v", documents)
	}
}

func TestRunReadClassifiesValidationAndStreamFailures(t *testing.T) {
	tests := []struct {
		name     string
		ctx      func() context.Context
		observer fakeObserver
		wantCode int
		wantKind string
	}{
		{
			name: "unknown service", ctx: context.Background,
			observer: fakeObserver{logs: func(context.Context, string, int, bool) (operations.Feed, error) {
				return operations.Feed{}, operations.UnknownServiceError{Name: "missing"}
			}},
			wantCode: 2, wantKind: "unknown_service",
		},
		{
			name: "missing exit", ctx: context.Background,
			observer: fakeObserver{logs: func(context.Context, string, int, bool) (operations.Feed, error) {
				return eventFeed(nil, operations.Event{Kind: operations.EventLog, Text: "partial"}), nil
			}},
			wantCode: 1, wantKind: "remote_exit_missing",
		},
		{
			name: "cancelled", ctx: cancelledContext,
			observer: fakeObserver{logs: func(context.Context, string, int, bool) (operations.Feed, error) {
				return eventFeed(nil), nil
			}},
			wantCode: 130, wantKind: "transport_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunRead(test.ctx(), Invocation{
				Command: CommandLogs, Host: "production", Service: "missing", Tail: 200,
			}, test.observer, &stdout, &stderr)
			if code != test.wantCode {
				t.Fatalf("code = %d, stderr = %q", code, stderr.String())
			}
			if kind := decodeErrorKind(t, stderr.Bytes()); kind != test.wantKind {
				t.Fatalf("kind = %q, want %q", kind, test.wantKind)
			}
			if test.name == "missing exit" {
				documents := decodeJSONLines(t, stdout.String())
				if len(documents) != 1 || documents[0]["type"] != "log" {
					t.Fatalf("missing-exit stdout = %#v", documents)
				}
			}
		})
	}
}

func TestRunReadReportsRemoteReadExitAfterTerminalEvent(t *testing.T) {
	observer := fakeObserver{logs: func(context.Context, string, int, bool) (operations.Feed, error) {
		return eventFeed(nil, operations.Event{Kind: operations.EventExit, ExitCode: 7}), nil
	}}
	var stdout, stderr bytes.Buffer
	code := RunRead(context.Background(), Invocation{
		Command: CommandLogs, Host: "production", Service: "web", Tail: 200,
	}, observer, &stdout, &stderr)
	if code != 1 || decodeErrorKind(t, stderr.Bytes()) != "remote_command_failed" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if got := decodeJSONLines(t, stdout.String())[0]["code"]; got != float64(7) {
		t.Fatalf("exit code event = %#v", got)
	}
}

func eventFeed(stopped *bool, events ...operations.Event) operations.Feed {
	stream := make(chan operations.Event, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return operations.Feed{
		Events: stream,
		Stop: func() {
			if stopped != nil {
				*stopped = true
			}
		},
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("invalid JSON %q: %v", raw, err)
	}
	return document
}

func decodeJSONLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var documents []map[string]any
	for line := range strings.Lines(raw) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		documents = append(documents, decodeJSON(t, []byte(line)))
	}
	return documents
}

func decodeErrorKind(t *testing.T, raw []byte) string {
	t.Helper()
	var document struct {
		Error struct {
			Kind string `json:"kind"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("invalid error JSON %q: %v", raw, err)
	}
	return document.Error.Kind
}

var _ Observer = fakeObserver{}

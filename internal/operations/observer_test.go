package operations

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/remote"
)

const serviceJSON = `{"Name":"app-web-1","Service":"web","State":"running","Status":"Up"}`

type execResult struct {
	output remote.ExecOutput
	err    error
}

type fakeExecutor struct {
	mu             sync.Mutex
	results        []execResult
	commands       []string
	streamCommands []string
	stream         func(context.Context, string) (<-chan remote.ExecEvent, error)
}

func (f *fakeExecutor) Exec(_ context.Context, command string) (remote.ExecOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	if len(f.results) == 0 {
		return remote.ExecOutput{}, errors.New("unexpected Exec")
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result.output, result.err
}

func (f *fakeExecutor) ExecStream(ctx context.Context, command string) (<-chan remote.ExecEvent, error) {
	f.mu.Lock()
	f.streamCommands = append(f.streamCommands, command)
	f.mu.Unlock()
	if f.stream == nil {
		return nil, errors.New("unexpected ExecStream")
	}
	return f.stream(ctx, command)
}

func TestStatusOwnsComposeFetchAndSoftRestartEnrichment(t *testing.T) {
	executor := &fakeExecutor{results: []execResult{
		{output: remote.ExecOutput{Stdout: []byte(serviceJSON), ExitCode: 0}},
		{output: remote.ExecOutput{Stdout: []byte("/app-web-1 3\n"), ExitCode: 1}},
	}}
	operator := NewHostOperator(executor, "/srv/app")

	services, err := operator.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Service != "web" || services[0].Restarts == nil || *services[0].Restarts != 3 {
		t.Fatalf("services = %+v", services)
	}
	want := []string{
		"cd '/srv/app' && docker compose ps --all --format json",
		"docker inspect --format '{{.Name}} {{.RestartCount}}' 'app-web-1'",
	}
	if !slices.Equal(executor.commands, want) {
		t.Fatalf("commands = %q, want %q", executor.commands, want)
	}
}

func TestStatusReportsPrimaryCommandFailure(t *testing.T) {
	executor := &fakeExecutor{results: []execResult{{
		output: remote.ExecOutput{Stderr: []byte("warning\nfatal compose error\n"), ExitCode: 7},
	}}}
	_, err := NewHostOperator(executor, "").Status(context.Background())
	if err == nil || err.Error() != "fatal compose error" {
		t.Fatalf("Status() error = %v", err)
	}
	var remoteFailure RemoteCommandError
	if !errors.As(err, &remoteFailure) || remoteFailure.ExitCode != 7 {
		t.Fatalf("Status() error type = %T, value %+v", err, err)
	}
}

func TestHostAndContainerSamplesPreserveSoftSemantics(t *testing.T) {
	executor := &fakeExecutor{results: []execResult{
		{output: remote.ExecOutput{Stdout: []byte("#load\n0.50 0.40 0.30\n#cpu\n4\n"), ExitCode: 1}},
		{output: remote.ExecOutput{Stdout: []byte(`{"Name":"app-web-1","CPUPerc":"12.34%"}`), ExitCode: 0}},
	}}
	operator := NewHostOperator(executor, "/srv/app")

	metrics, err := operator.HostMetrics(context.Background())
	if err != nil || metrics.Load1 != 0.5 || metrics.CPUs != 4 {
		t.Fatalf("HostMetrics() = %+v, %v", metrics, err)
	}
	stats, err := operator.ContainerStats(context.Background())
	if err != nil || len(stats) != 1 || stats[0].Name != "app-web-1" {
		t.Fatalf("ContainerStats() = %+v, %v", stats, err)
	}
}

func TestLogsValidateServiceAndAssembleTypedEvents(t *testing.T) {
	remoteEvents := make(chan remote.ExecEvent, 8)
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecStdout, Data: []byte("first pa")}
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecStdout, Data: []byte("rt\n\nlast")}
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecStderr, Data: []byte("warn\ning")}
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecExit, ExitCode: 4}
	close(remoteEvents)

	executor := &fakeExecutor{
		results: []execResult{{output: remote.ExecOutput{Stdout: []byte(serviceJSON), ExitCode: 0}}},
		stream: func(context.Context, string) (<-chan remote.ExecEvent, error) {
			return remoteEvents, nil
		},
	}
	feed, err := NewHostOperator(executor, "/srv/app").Logs(context.Background(), "web", 200, true)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(feed)
	want := []Event{
		{Kind: EventLog, Text: "first part"},
		{Kind: EventLog, Text: ""},
		{Kind: EventStderr, Text: "warn"},
		{Kind: EventLog, Text: "last"},
		{Kind: EventStderr, Text: "ing"},
		{Kind: EventExit, ExitCode: 4},
	}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	wantCommand := "cd '/srv/app' && docker compose logs --follow --no-color --no-log-prefix --tail 200 'web'"
	if !slices.Equal(executor.streamCommands, []string{wantCommand}) {
		t.Fatalf("stream commands = %q", executor.streamCommands)
	}
}

func TestLogsRejectInvalidTailAndUnknownServiceBeforeStreaming(t *testing.T) {
	operator := NewHostOperator(&fakeExecutor{}, "")
	if _, err := operator.Logs(context.Background(), "web", 0, true); err == nil {
		t.Fatal("zero tail unexpectedly accepted")
	}

	executor := &fakeExecutor{results: []execResult{{
		output: remote.ExecOutput{Stdout: []byte(serviceJSON), ExitCode: 0},
	}}}
	_, err := NewHostOperator(executor, "").Logs(context.Background(), "db", 200, true)
	var unknown UnknownServiceError
	if !errors.As(err, &unknown) || unknown.Name != "db" {
		t.Fatalf("Logs() error = %v", err)
	}
	if len(executor.streamCommands) != 0 {
		t.Fatalf("started stream for unknown service: %q", executor.streamCommands)
	}
}

func TestFollowStatsParsesSamplesAndSkipsFraming(t *testing.T) {
	remoteEvents := make(chan remote.ExecEvent, 8)
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecStdout, Data: []byte("\nnot json\n")}
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecStdout, Data: []byte(`{"Name":"app-web-1","CPUPerc":"5.00%"}` + "\n")}
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecStderr, Data: []byte("daemon warning\n")}
	remoteEvents <- remote.ExecEvent{Kind: remote.ExecExit, ExitCode: 0}
	close(remoteEvents)

	executor := &fakeExecutor{stream: func(context.Context, string) (<-chan remote.ExecEvent, error) {
		return remoteEvents, nil
	}}
	feed, err := NewHostOperator(executor, "/srv/app").FollowStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	events := collect(feed)
	if len(events) != 3 || events[0].Kind != EventStats || events[0].Stats.Name != "app-web-1" ||
		events[1].Kind != EventStderr || events[2] != (Event{Kind: EventExit, ExitCode: 0}) {
		t.Fatalf("events = %+v", events)
	}
	if len(executor.streamCommands) != 1 || !strings.Contains(executor.streamCommands[0], "docker stats") {
		t.Fatalf("stream command = %q", executor.streamCommands)
	}
}

func TestFeedStopCancelsAndCloses(t *testing.T) {
	executor := &fakeExecutor{stream: func(ctx context.Context, _ string) (<-chan remote.ExecEvent, error) {
		events := make(chan remote.ExecEvent)
		go func() {
			<-ctx.Done()
			close(events)
		}()
		return events, nil
	}}
	feed, err := NewHostOperator(executor, "").AdHoc(context.Background(), "watch")
	if err != nil {
		t.Fatal(err)
	}
	feed.Stop()
	feed.Stop()
	done := make(chan struct{})
	go func() {
		for range feed.Events {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("feed did not close after Stop")
	}
}

func collect(feed Feed) []Event {
	var events []Event
	for event := range feed.Events {
		events = append(events, event)
	}
	return events
}

var _ Executor = (*fakeExecutor)(nil)

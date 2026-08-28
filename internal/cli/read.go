package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
)

// Observer is the complete interface visible to remote read commands. In
// particular, it cannot execute an arbitrary command.
type Observer interface {
	Status(context.Context) ([]compose.Service, error)
	HostMetrics(context.Context) (host.Metrics, error)
	ContainerStats(context.Context) ([]compose.ContainerStats, error)
	Logs(context.Context, string, int, bool) (operations.Feed, error)
	FollowStats(context.Context) (operations.Feed, error)
}

// RunRead invokes one typed observation and presents it as JSON or JSONL.
func RunRead(
	ctx context.Context,
	invocation Invocation,
	observer Observer,
	stdout io.Writer,
	stderr io.Writer,
) int {
	switch invocation.Command {
	case CommandStatus:
		return runStatus(ctx, invocation, observer, stdout, stderr)
	case CommandStats:
		if invocation.Follow {
			return runFollowStats(ctx, invocation, observer, stdout, stderr)
		}
		return runStats(ctx, invocation, observer, stdout, stderr)
	case CommandLogs:
		return runLogs(ctx, invocation, observer, stdout, stderr)
	default:
		return Report(stderr, OperationalFailure(invocation.Operation(), "unsupported_operation",
			fmt.Errorf("%s is not a read operation", invocation.Command)))
	}
}

func runStatus(ctx context.Context, invocation Invocation, observer Observer, stdout, stderr io.Writer) int {
	services, err := observer.Status(ctx)
	if err != nil {
		return Report(stderr, ReadFailure(invocation.Operation(), err))
	}
	return writeSuccess(stdout, stderr, invocation.Operation(), statusDocument{
		SchemaVersion: schemaVersion,
		Host:          invocation.Host,
		Services:      servicePayloads(services),
	})
}

func runStats(ctx context.Context, invocation Invocation, observer Observer, stdout, stderr io.Writer) int {
	metrics, err := observer.HostMetrics(ctx)
	if err != nil {
		return Report(stderr, ReadFailure(invocation.Operation(), err))
	}
	containers, err := observer.ContainerStats(ctx)
	if err != nil {
		return Report(stderr, ReadFailure(invocation.Operation(), err))
	}
	return writeSuccess(stdout, stderr, invocation.Operation(), statsDocument{
		SchemaVersion: schemaVersion,
		Host:          invocation.Host,
		HostMetrics:   projectHostMetrics(metrics),
		Containers:    containerPayloads(containers),
	})
}

func runFollowStats(ctx context.Context, invocation Invocation, observer Observer, stdout, stderr io.Writer) int {
	metrics, err := observer.HostMetrics(ctx)
	if err != nil {
		return Report(stderr, ReadFailure(invocation.Operation(), err))
	}
	if err := writeJSON(stdout, hostStatsEvent{
		SchemaVersion: schemaVersion,
		Type:          "host_stats",
		Stats:         projectHostMetrics(metrics),
	}); err != nil {
		return Report(stderr, OperationalFailure(invocation.Operation(), "output_error", err))
	}

	feed, err := observer.FollowStats(ctx)
	if err != nil {
		return Report(stderr, ReadFailure(invocation.Operation(), err))
	}
	return consumeFeed(ctx, invocation.Operation(), feed, stdout, stderr, statsEventPayload, reportRemoteExit)
}

func runLogs(ctx context.Context, invocation Invocation, observer Observer, stdout, stderr io.Writer) int {
	feed, err := observer.Logs(ctx, invocation.Service, invocation.Tail, invocation.Follow)
	if err != nil {
		return Report(stderr, ReadFailure(invocation.Operation(), err))
	}
	return consumeFeed(ctx, invocation.Operation(), feed, stdout, stderr, logEventPayload, reportRemoteExit)
}

type eventProjector func(operations.Event) (any, bool)

type exitPolicy uint8

const (
	reportRemoteExit exitPolicy = iota
	propagateRemoteExit
)

func consumeFeed(
	ctx context.Context,
	operation string,
	feed operations.Feed,
	stdout io.Writer,
	stderr io.Writer,
	project eventProjector,
	policy exitPolicy,
) int {
	defer feed.Stop()
	for event := range feed.Events {
		if event.Kind == operations.EventExit {
			return consumeExit(ctx, operation, event.ExitCode, policy, stdout, stderr)
		}
		payload, ok := project(event)
		if !ok {
			continue
		}
		if err := writeJSON(stdout, payload); err != nil {
			return Report(stderr, OperationalFailure(operation, "output_error", err))
		}
	}
	return reportFeedEnd(ctx, operation, stderr)
}

func consumeExit(
	ctx context.Context,
	operation string,
	code int,
	policy exitPolicy,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if code < 0 {
		return reportFeedEnd(ctx, operation, stderr)
	}
	if err := writeJSON(stdout, exitEvent{
		SchemaVersion: schemaVersion,
		Type:          "exit",
		Code:          code,
	}); err != nil {
		return Report(stderr, OperationalFailure(operation, "output_error", err))
	}
	if code == 0 {
		return 0
	}
	if policy == propagateRemoteExit {
		return code
	}
	return Report(stderr, ReadFailure(operation, operations.RemoteCommandError{
		ExitCode: code,
		Message:  fmt.Sprintf("remote command failed with exit code %d", code),
	}))
}

func reportFeedEnd(ctx context.Context, operation string, stderr io.Writer) int {
	if err := ctx.Err(); err != nil {
		return Report(stderr, StreamFailure(operation, err))
	}
	return Report(stderr, StreamFailure(operation, operations.RemoteCommandError{
		ExitCode: -1,
		Message:  "remote command finished without an exit status",
	}))
}

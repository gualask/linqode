package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gualask/linqode/internal/operations"
)

// Mutator is the typed mutation surface visible to machine commands. It has no
// arbitrary-command method.
type Mutator interface {
	Action(context.Context, operations.ServiceAction, string) (operations.Feed, error)
	Script(context.Context, string) (operations.Feed, error)
}

// SafeOperator is the complete connected capability available to the machine
// adapter. Human-only ad-hoc execution is intentionally absent.
type SafeOperator interface {
	Observer
	Mutator
}

// RunMutation invokes one typed mutation and streams JSONL events.
func RunMutation(
	ctx context.Context,
	invocation Invocation,
	mutator Mutator,
	stdout io.Writer,
	stderr io.Writer,
) int {
	var (
		feed operations.Feed
		err  error
	)
	switch invocation.Command {
	case CommandRestart:
		feed, err = mutator.Action(ctx, operations.ActionRestart, invocation.Service)
	case CommandStop:
		feed, err = mutator.Action(ctx, operations.ActionStop, invocation.Service)
	case CommandStart:
		feed, err = mutator.Action(ctx, operations.ActionStart, invocation.Service)
	case CommandScript:
		feed, err = mutator.Script(ctx, invocation.ScriptName)
	default:
		return Report(stderr, OperationalFailure(invocation.Operation(), "unsupported_operation",
			fmt.Errorf("%s is not a mutation operation", invocation.Command)))
	}
	if err != nil {
		return Report(stderr, MutationFailure(invocation.Operation(), err))
	}
	return consumeFeed(ctx, invocation.Operation(), feed, stdout, stderr,
		mutationEventPayload, propagateRemoteExit)
}

func mutationEventPayload(event operations.Event) (any, bool) {
	switch event.Kind {
	case operations.EventStdout, operations.EventStderr:
		return textEvent{SchemaVersion: schemaVersion, Type: string(event.Kind), Data: event.Text}, true
	default:
		return nil, false
	}
}

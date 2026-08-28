package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/remote"
)

// ReadPreflight rejects configured capabilities before a machine connection
// is opened.
func ReadPreflight(invocation Invocation, hostMetrics bool) *Failure {
	if invocation.Command == CommandStats && !hostMetrics {
		return OperationalFailure(invocation.Operation(), "unsupported_operation",
			fmt.Errorf("stats are disabled for host %q", invocation.Host))
	}
	return nil
}

// SelectionFailure classifies strict configured-name selection.
func SelectionFailure(operation string, err error) *Failure {
	var unknown operations.UnknownHostError
	var unknownScript operations.UnknownScriptError
	switch {
	case errors.As(err, &unknown):
		return inputFailure(operation, "unknown_host", err.Error())
	case errors.As(err, &unknownScript):
		return inputFailure(operation, "unknown_script", err.Error())
	}
	return OperationalFailure(operation, "config_error", err)
}

// ConnectionFailure classifies failures that happen before an SSH session is
// established.
func ConnectionFailure(operation string, err error) *Failure {
	var unknown *remote.UnknownHostKeyError
	var changed *remote.HostKeyChangedError
	var passphrase *remote.PassphraseRequiredError
	var auth *remote.AuthFailedError
	var badPassphrase *remote.BadPassphraseError

	switch {
	case errors.Is(err, context.Canceled):
		return interruptedFailure(operation)
	case errors.As(err, &unknown):
		return OperationalFailure(operation, "unknown_host_key", err)
	case errors.As(err, &changed):
		return OperationalFailure(operation, "changed_host_key", err)
	case errors.As(err, &passphrase):
		return OperationalFailure(operation, "passphrase_required", err)
	case errors.As(err, &auth), errors.As(err, &badPassphrase):
		return OperationalFailure(operation, "authentication_failed", err)
	default:
		return OperationalFailure(operation, "connection_failed", err)
	}
}

// ReadFailure classifies an error after a read operation has been selected.
func ReadFailure(operation string, err error) *Failure {
	var unknownService operations.UnknownServiceError
	var invalidTail operations.InvalidTailError
	var parse operations.ParseError
	var remoteCommand operations.RemoteCommandError

	switch {
	case errors.As(err, &unknownService):
		return inputFailure(operation, "unknown_service", err.Error())
	case errors.As(err, &invalidTail):
		return inputFailure(operation, "invalid_argument", err.Error())
	case errors.As(err, &parse):
		return OperationalFailure(operation, "parse_failed", err)
	case errors.As(err, &remoteCommand) && remoteCommand.ExitCode >= 0:
		return OperationalFailure(operation, "remote_command_failed", err)
	default:
		return StreamFailure(operation, err)
	}
}

// MutationFailure classifies a failure before a typed mutation stream starts.
func MutationFailure(operation string, err error) *Failure {
	var unknownService operations.UnknownServiceError
	var unknownScript operations.UnknownScriptError
	var invalidAction operations.InvalidActionError
	var remoteCommand operations.RemoteCommandError

	switch {
	case errors.As(err, &unknownService):
		return inputFailure(operation, "unknown_service", err.Error())
	case errors.As(err, &unknownScript):
		return inputFailure(operation, "unknown_script", err.Error())
	case errors.As(err, &invalidAction):
		return inputFailure(operation, "invalid_argument", err.Error())
	case errors.As(err, &remoteCommand) && remoteCommand.ExitCode >= 0:
		return OperationalFailure(operation, "remote_command_failed", err)
	default:
		return StreamFailure(operation, err)
	}
}

// StreamFailure owns failures common to read and mutation feeds.
func StreamFailure(operation string, err error) *Failure {
	var remoteCommand operations.RemoteCommandError
	switch {
	case errors.Is(err, context.Canceled):
		return interruptedFailure(operation)
	case errors.As(err, &remoteCommand) && remoteCommand.ExitCode < 0:
		return OperationalFailure(operation, "remote_exit_missing", err)
	default:
		return OperationalFailure(operation, "transport_failed", err)
	}
}

func interruptedFailure(operation string) *Failure {
	return &Failure{
		Kind:      "transport_failed",
		Message:   "operation interrupted",
		Operation: operation,
		ExitCode:  130,
	}
}

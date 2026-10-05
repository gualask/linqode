package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/probe"
	"github.com/gualask/linqode/internal/remote"
)

// Prober establishes what a connected host can be asked for. It is an
// observation like any other on this boundary — it reads and changes nothing —
// and it is here so a machine command can say which of its own preconditions
// the host does not meet, instead of returning the opaque non-zero exit that a
// compose command hitting a socket it may not open produces.
type Prober interface {
	Probe(context.Context) (probe.Result, error)
}

// needsCompose reports whether a command cannot work without a compose project
// behind it. `script` is the exception, and the same one the TUI makes: a
// configured script is a command the operator wrote, and it has never needed a
// daemon.
//
// `stats` is on the list although its host half owes docker nothing, and the
// TUI keeps its meters on the same host. The difference is the reader. A
// screen can say why a panel is missing; a document promising host metrics
// and containers that came back with an empty `containers` would tell a
// machine "nothing is running", which is a different and false answer. Named
// as unavailable, the caller can tell.
func needsCompose(command Command) bool {
	switch command {
	case CommandStatus, CommandStats, CommandLogs,
		CommandRestart, CommandStop, CommandStart:
		return true
	default:
		return false
	}
}

// ComposePreflight rejects a command this host cannot run, after connecting and
// before running it.
//
// It costs one round trip on a session that has already paid for an SSH
// handshake, and it buys the difference between `remote_command_failed` with
// whatever the shell happened to say and a named condition a caller can branch
// on. A probe that fails, or that establishes nothing, rejects nothing: the
// command then runs and reports its own failure, which is where this started.
func ComposePreflight(ctx context.Context, invocation Invocation, prober Prober) *Failure {
	if !needsCompose(invocation.Command) {
		return nil
	}
	result, err := prober.Probe(ctx)
	if err != nil || result.CanCompose() {
		return nil
	}
	return &Failure{
		Kind:      composeFailureKind(result),
		Message:   result.ComposeUnavailable(),
		Operation: invocation.Operation(),
		ExitCode:  1,
	}
}

// composeFailureKind names the condition, so a caller can tell apart the three
// that need different things done about them: a host to install docker on, an
// account to add to a group, and a path to correct in the config.
func composeFailureKind(result probe.Result) string {
	switch result.Docker {
	case probe.DockerDenied:
		return "docker_permission_denied"
	case probe.DockerAbsent, probe.DockerUnreachable:
		return "docker_unavailable"
	}
	if result.Directory == probe.DirectoryMissing {
		return "compose_dir_missing"
	}
	return "compose_unavailable"
}

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
	var localHost operations.LocalHostError
	var unknownScript operations.UnknownScriptError
	switch {
	case errors.As(err, &unknown):
		return inputFailure(operation, "unknown_host", err.Error())
	// Configured, and still not a name this interface answers to. Saying
	// "unknown" instead would send the caller looking for a typo.
	case errors.As(err, &localHost):
		return inputFailure(operation, "local_host", err.Error())
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
	var typeNotRecorded *remote.HostKeyTypeNotRecordedError
	var passphrase *remote.PassphraseRequiredError
	var auth *remote.AuthFailedError
	var badPassphrase *remote.BadPassphraseError

	switch {
	case errors.Is(err, context.Canceled):
		return interruptedFailure(operation)
	case errors.As(err, &unknown):
		return OperationalFailure(operation, "unknown_host_key", err)
	case errors.As(err, &changed), errors.As(err, &typeNotRecorded):
		// A key of a type known_hosts does not pin is refused for the same
		// reason a different key is: only a person can verify it.
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

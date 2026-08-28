package operations

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/remote"
)

const MaxLogTail = 10_000

// Executor is the SSH behavior needed by shared operations.
type Executor interface {
	Exec(context.Context, string) (remote.ExecOutput, error)
	ExecStream(context.Context, string) (<-chan remote.ExecEvent, error)
}

// HostOperator owns typed remote workflows for one connected Compose project.
// It contains no presentation behavior.
type HostOperator struct {
	executor   Executor
	composeDir string
	scripts    map[string]string
}

func NewHostOperator(executor Executor, composeDir string, scripts ...Script) *HostOperator {
	commands := make(map[string]string, len(scripts))
	for _, script := range scripts {
		commands[script.Name] = script.Command
	}
	return &HostOperator{executor: executor, composeDir: composeDir, scripts: commands}
}

// NewConfiguredHostOperator preserves the configured script boundary without
// exposing script command bodies to the composition root.
func NewConfiguredHostOperator(executor Executor, configured ConfiguredHost) *HostOperator {
	return &HostOperator{
		executor: executor, composeDir: configured.ComposeDir,
		scripts: maps.Clone(configured.scripts),
	}
}

// Status returns the current Compose services, enriched with restart counts
// when the soft inspect succeeds.
func (o *HostOperator) Status(ctx context.Context) ([]compose.Service, error) {
	services, err := o.services(ctx)
	if err != nil {
		return nil, err
	}
	o.addRestarts(ctx, services)
	return services, nil
}

// HostMetrics samples the remote machine. Individual unavailable metric
// sources remain soft failures, matching host.Parse and the existing TUI.
func (o *HostOperator) HostMetrics(ctx context.Context) (host.Metrics, error) {
	out, err := o.executor.Exec(ctx, host.Command())
	if err != nil {
		return host.Metrics{}, err
	}
	metrics, err := host.Parse(out.Stdout)
	if err != nil {
		return host.Metrics{}, ParseError{Err: err}
	}
	return metrics, nil
}

// ContainerStats returns one resource sample for the project's containers.
func (o *HostOperator) ContainerStats(ctx context.Context) ([]compose.ContainerStats, error) {
	out, err := o.executor.Exec(ctx, compose.StatsSampleCommand(o.composeDir))
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, commandFailure(out)
	}
	return compose.ParseStatsSample(out.Stdout), nil
}

// Logs starts a bounded or followed log stream for one current service.
func (o *HostOperator) Logs(ctx context.Context, service string, tail int, follow bool) (Feed, error) {
	if tail < 1 || tail > MaxLogTail {
		return Feed{}, InvalidTailError{Tail: tail}
	}
	if err := o.requireService(ctx, service); err != nil {
		return Feed{}, err
	}
	return o.startFeed(ctx, compose.LogsCommand(o.composeDir, service, tail, follow), logLine)
}

// FollowStats starts the live resource stream for the project's containers.
func (o *HostOperator) FollowStats(ctx context.Context) (Feed, error) {
	return o.startFeed(ctx, compose.StatsCommand(o.composeDir), statsLine)
}

// AdHoc starts a human-authorized raw command for the TUI's `!` path. Machine
// adapters must receive a narrower interface that omits this method.
func (o *HostOperator) AdHoc(ctx context.Context, command string) (Feed, error) {
	return o.startFeed(ctx, command, stdoutLine)
}

func (o *HostOperator) services(ctx context.Context) ([]compose.Service, error) {
	out, err := o.executor.Exec(ctx, compose.PsCommand(o.composeDir))
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, commandFailure(out)
	}
	services, err := compose.ParsePS(out.Stdout)
	if err != nil {
		return nil, ParseError{Err: err}
	}
	return services, nil
}

func (o *HostOperator) requireService(ctx context.Context, service string) error {
	services, err := o.services(ctx)
	if err != nil {
		return err
	}
	for _, current := range services {
		if current.Service == service {
			return nil
		}
	}
	return UnknownServiceError{Name: service}
}

// addRestarts is deliberately soft: a disappearing container or an inspect
// failure leaves the optional restart column unknown without failing status.
func (o *HostOperator) addRestarts(ctx context.Context, services []compose.Service) {
	names := make([]string, 0, len(services))
	for _, service := range services {
		if service.Name != "" {
			names = append(names, service.Name)
		}
	}
	command := compose.InspectRestartsCommand(names)
	if command == "" {
		return
	}
	out, err := o.executor.Exec(ctx, command)
	if err != nil {
		return
	}
	compose.ApplyRestarts(services, compose.ParseRestarts(out.Stdout))
}

func commandFailure(out remote.ExecOutput) RemoteCommandError {
	lines := strings.Split(strings.TrimRight(string(out.Stderr), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return RemoteCommandError{ExitCode: out.ExitCode, Message: line}
		}
	}
	if out.ExitCode >= 0 {
		return RemoteCommandError{
			ExitCode: out.ExitCode,
			Message:  fmt.Sprintf("remote command failed with exit code %d", out.ExitCode),
		}
	}
	return RemoteCommandError{ExitCode: -1, Message: "remote command failed without an exit status"}
}

type RemoteCommandError struct {
	ExitCode int
	Message  string
}

func (e RemoteCommandError) Error() string { return e.Message }

// ParseError distinguishes invalid remote output from transport failures
// without changing the human-facing error message.
type ParseError struct {
	Err error
}

func (e ParseError) Error() string { return e.Err.Error() }
func (e ParseError) Unwrap() error { return e.Err }

type UnknownServiceError struct {
	Name string
}

func (e UnknownServiceError) Error() string {
	return fmt.Sprintf("service %q is not in the Compose project", e.Name)
}

type InvalidTailError struct {
	Tail int
}

func (e InvalidTailError) Error() string {
	return fmt.Sprintf("log tail must be between 1 and %d, got %d", MaxLogTail, e.Tail)
}

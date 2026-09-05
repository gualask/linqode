package operations

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/probe"
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

// Probe establishes what this host can be asked for, once, before anything
// asks it. Unlike every other reading here it is not sampled: what it
// establishes does not change while a session is open, and a host that gains
// a docker group mid-session is a reconnect, not a refresh.
//
// A transport failure is returned, because it means the session itself is in
// trouble. Anything else the host said is Parse's business, and Parse does not
// fail: an answer nobody can make sense of leaves every capability Unknown,
// which reads as "carry on".
func (o *HostOperator) Probe(ctx context.Context) (probe.Result, error) {
	out, err := o.executor.Exec(ctx, probe.Command(o.composeDir))
	if err != nil {
		return probe.Result{}, err
	}
	// The exit code is deliberately ignored. The batch ends with a grep that
	// finds nothing on a host with no /etc/os-release, and a non-zero status
	// there says nothing about the four sections that matter.
	return probe.Parse(out.Stdout, o.composeDir), nil
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

// HostProcesses reads the machine's process table. It is an on-demand
// reading, not a sampled one: it costs about 220 bytes per process, which is
// worth paying while an operator is looking at the list and not otherwise.
//
// A process that exits between the directory listing and the read is one row
// missing, never an error: the kernel's own view of what is running changes
// while it is being read, and that is not a failure.
func (o *HostOperator) HostProcesses(ctx context.Context) (host.ProcessSample, error) {
	out, err := o.executor.Exec(ctx, host.ProcessCommand())
	if err != nil {
		return host.ProcessSample{}, err
	}
	return host.ParseProcessSample(out.Stdout), nil
}

// GPUs reads the machine's graphics cards. On-demand, because one of the two
// vendors it asks answers only through nvidia-smi, which initialises a driver
// context rather than reading a file — hundreds of milliseconds, and worse
// with persistence mode off.
//
// A host with neither an AMD card nor the NVIDIA tool matches no glob and
// runs no second command, so the reading costs a shell fork and nothing else.
func (o *HostOperator) GPUs(ctx context.Context) ([]host.GPU, error) {
	out, err := o.executor.Exec(ctx, host.GPUCommand())
	if err != nil {
		return nil, err
	}
	return host.ParseGPUs(out.Stdout), nil
}

// DiskUsage asks the daemon what it is holding: images, containers, volumes
// and build cache. It is not scoped to the project and cannot be — those are
// the daemon's, shared with everything else on the host, which is exactly
// what makes the answer worth having.
//
// Slow by nature: the daemon walks the image store to answer. It is an
// on-demand reading for that reason.
func (o *HostOperator) DiskUsage(ctx context.Context) ([]compose.DiskUsage, error) {
	out, err := o.executor.Exec(ctx, compose.SystemDFCommand())
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, commandFailure(out)
	}
	return compose.ParseSystemDF(out.Stdout), nil
}

// ContainerCgroups reads the project's container resources from the kernel:
// the cgroup counters, and the network counters of the given processes. It is
// what the TUI samples, because it costs a couple of milliseconds against the
// ~2 s `docker stats` needs to derive a CPU percentage the client can derive
// itself from two readings.
//
// Missing sources stay soft, as everywhere else that reads /proc and /sys: a
// host without the pids controller reports every other counter.
func (o *HostOperator) ContainerCgroups(ctx context.Context, pids []int) (compose.CgroupSample, error) {
	out, err := o.executor.Exec(ctx, compose.StatsCgroupCommand(pids))
	if err != nil {
		return compose.CgroupSample{}, err
	}
	return compose.ParseCgroupSample(out.Stdout, time.Now()), nil
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

// Watch streams the daemon's changes to the given project's containers: what
// the status view listens to instead of asking `ps` on a timer. The project
// name comes from the service list, so this is started once there is one.
func (o *HostOperator) Watch(ctx context.Context, project string) (Feed, error) {
	command := compose.EventsCommand(project)
	if command == "" {
		return Feed{}, fmt.Errorf("no compose project to watch")
	}
	return o.startFeed(ctx, command, changeLine)
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
	command := compose.InspectCommand(names)
	if command == "" {
		return
	}
	out, err := o.executor.Exec(ctx, command)
	if err != nil {
		return
	}
	compose.ApplyInspected(services, compose.ParseInspected(out.Stdout))
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

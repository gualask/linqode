package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/local"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/probe"
	"github.com/gualask/linqode/internal/remote"
	"github.com/gualask/linqode/internal/tui"
)

// transport is how one run reaches its host, and how the header names it.
// Everything below is written against operations.Executor and cannot tell
// which one it holds, which is the whole point of the seam.
type transport struct {
	executor operations.Executor
	target   string
	close    func()
	// local is whether the machine being watched is this one. It gates the
	// native reader, which must never be asked about a host reached over
	// SSH: it would answer about the wrong machine, confidently.
	local bool
}

// openTransport reaches the host, or the machine this is running on.
//
// The local branch skips the "Connecting to …" line rather than printing a
// version of it: that line exists because opening an SSH session takes a
// noticeable moment and can fail in ways worth naming, and neither is true
// of starting a process here.
func openTransport(ctx context.Context, spec string, stderr io.Writer) (transport, error) {
	if spec == config.LocalSpec && runtime.GOOS == "windows" {
		// Every command runs under `sh -c` and every reading is a Unix
		// one; there is no local target to offer here, and saying so beats
		// a screen of commands that cannot start.
		return transport{}, errors.New("the local target is not supported on Windows; connect to a host over SSH")
	}
	if spec == config.LocalSpec {
		return transport{
			executor: local.New(),
			target:   config.LocalSpec,
			close:    func() {},
			local:    true,
		}, nil
	}

	target, err := remote.Resolve(spec)
	if err != nil {
		return transport{}, err
	}
	fmt.Fprintf(stderr, "Connecting to %s@%s:%d ...\n",
		target.User, target.DisplayHost, target.Port)
	session, err := remote.Connect(ctx, target, terminalPrompter{ctx: ctx})
	if err != nil {
		return transport{}, err
	}
	return transport{
		executor: session,
		target:   target.User + "@" + target.DisplayHost,
		close:    session.Close,
	}, nil
}

// runTUI connects and launches the interactive TUI.
func runTUI(ctx context.Context, configPath, hostArg string, stderr io.Writer) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	sel, err := cfg.Select(hostArg)
	if err != nil {
		return err
	}
	link, err := openTransport(ctx, sel.Spec, stderr)
	if err != nil {
		return err
	}
	defer link.close()
	// Every feed the screen opens runs on this context, and the screen's
	// return is the end of all of them: cancelling it here, before the
	// session closes (defers run in reverse), ends whatever a view left
	// running rather than leaving it to be cut off underneath.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	operator := operations.NewHostOperator(link.executor, sel.ComposeDir, sel.Scripts...)

	// One round trip establishing what this host can be asked for, before
	// anything asks it. A probe that could not run establishes nothing, and
	// nothing it did not establish turns anything off: the zero result reads
	// as "carry on", and whatever is actually wrong is still there to be
	// reported by the command that hits it, with its own message.
	capabilities, err := operator.Probe(ctx)
	if err != nil {
		capabilities = probe.Result{}
	}

	info := tui.Info{
		Target:             link.target,
		ComposeDir:         sel.ComposeDir,
		Scripts:            sel.Scripts,
		OS:                 capabilities.OS,
		DockerEndpoint:     capabilities.DockerEndpoint(),
		ComposeUnavailable: capabilities.ComposeUnavailable(),
	}
	if err := ctx.Err(); err != nil {
		return err // a signal while connecting: there is no screen to open
	}
	return tui.Run(ctx, info, newBackend(ctx, operator, link.local, sel.HostMetrics, capabilities))
}

// newBackend wires what the screen may ask this host for. A fetch left nil
// leaves the feature it feeds out of the view, which is how a capability the
// probe did not find, or a reading the config turned off, is expressed.
func newBackend(ctx context.Context, operator *operations.HostOperator, local, hostMetrics bool,
	capabilities probe.Result) tui.Backend {
	// Scripts and the `!` prompt are here whatever the probe found: neither
	// has ever needed a daemon, and a host without docker is still a host
	// worth having a terminal on.
	backend := tui.Backend{
		Script: func(name string) (operations.Feed, error) {
			return operator.Script(ctx, name)
		},
		AdHoc: func(command string) (operations.Feed, error) {
			return operator.AdHoc(ctx, command)
		},
	}
	if capabilities.CanCompose() {
		wireCompose(ctx, &backend, operator)
	}
	// Every resource fetch rides on the same switch: one host that wants no
	// extra commands wants none of them. See `host_metrics` in the config
	// format.
	if hostMetrics {
		wireMachine(ctx, &backend, operator, local, capabilities)
		// The container readings are addressed by container, so they have
		// nothing to ask about where there is no service list. What docker
		// holds on disk goes with them for a different reason: it is drawn
		// under the services table, and a host with no compose has no table
		// on screen — the machine takes the body, and the system view is
		// about the machine and nothing else.
		if capabilities.CanCompose() {
			wireContainerReadings(ctx, &backend, operator)
		}
	}
	return backend
}

// wireCompose is the project: its services, their logs and lifecycle, and
// what the daemon reports happening to them.
func wireCompose(ctx context.Context, backend *tui.Backend, operator *operations.HostOperator) {
	backend.Services = func() ([]compose.Service, error) {
		return operator.Status(ctx)
	}
	backend.Logs = func(service string, tail int) (operations.Feed, error) {
		return operator.Logs(ctx, service, tail, true)
	}
	backend.ActionPreview = operator.ActionPreview
	backend.Action = func(action operations.ServiceAction, service string) (operations.Feed, error) {
		return operator.Action(ctx, action, service)
	}
	// The event stream is the daemon's rather than compose's, but what it
	// is scoped to is a compose project, and there is no project to name
	// without a service list to read it off.
	backend.Watch = func(project string) (operations.Feed, error) {
		return operator.Watch(ctx, project)
	}
}

// wireMachine is the machine's own readings, which come off /proc and /sys
// and owe docker nothing, so they survive every finding the probe can make.
func wireMachine(ctx context.Context, backend *tui.Backend, operator *operations.HostOperator,
	local bool, capabilities probe.Result) {
	switch {
	case capabilities.CanReadProc():
		backend.Host = func() (host.Metrics, error) {
			return operator.HostMetrics(ctx)
		}
		backend.Processes = func() (host.ProcessSample, error) {
			return operator.HostProcesses(ctx)
		}
	// No /proc, and the machine is this one: the readings are taken
	// natively instead. This is the only branch in the program where a
	// reading does not ride on the Executor, and the `local` guard is
	// what keeps it honest — a Mac reached over SSH lands below, with
	// the filesystems it can still answer for and nothing invented.
	case local:
		backend.Host = func() (host.Metrics, error) {
			return host.Sample(ctx)
		}
		backend.Processes = func() (host.ProcessSample, error) {
			return host.SampleProcesses(ctx)
		}
	// No /proc and not this machine. The batch still answers `df`, and
	// the screen draws no meter it has no number for; the process table
	// would be a panel that could only ever open empty, so it is left
	// off.
	default:
		backend.Host = func() (host.Metrics, error) {
			return operator.HostMetrics(ctx)
		}
	}
	backend.GPUs = func() ([]host.GPU, error) {
		return operator.GPUs(ctx)
	}
}

// wireContainerReadings is what the services cost: their counters, the live
// stream, and what docker holds on disk.
func wireContainerReadings(ctx context.Context, backend *tui.Backend, operator *operations.HostOperator) {
	backend.DiskUsage = func() ([]compose.DiskUsage, error) {
		return operator.DiskUsage(ctx)
	}
	backend.Stats = func(services []compose.Service) (compose.CgroupSample, error) {
		return operator.ContainerCgroups(ctx, runningPIDs(services))
	}
	backend.LiveStats = func() (operations.Feed, error) {
		return operator.FollowStats(ctx)
	}
}

// runningPIDs is the main process of every service that has one: a stopped
// container has no process to read counters off.
func runningPIDs(services []compose.Service) []int {
	pids := make([]int, 0, len(services))
	for _, service := range services {
		if service.Pid > 0 {
			pids = append(pids, service.Pid)
		}
	}
	return pids
}

package main

import (
	"context"
	"fmt"
	"io"

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
}

// openTransport reaches the host, or the machine this is running on.
//
// The local branch skips the "Connecting to …" line rather than printing a
// version of it: that line exists because opening an SSH session takes a
// noticeable moment and can fail in ways worth naming, and neither is true
// of starting a process here.
func openTransport(ctx context.Context, spec string, stderr io.Writer) (transport, error) {
	if spec == config.LocalSpec {
		return transport{
			executor: local.New(),
			target:   config.LocalSpec,
			close:    func() {},
		}, nil
	}

	target, err := remote.Resolve(spec)
	if err != nil {
		return transport{}, err
	}
	fmt.Fprintf(stderr, "Connecting to %s@%s:%d ...\n",
		target.User, target.DisplayHost, target.Port)
	session, err := remote.Connect(ctx, target, terminalPrompter{})
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
		ComposeUnavailable: capabilities.ComposeUnavailable(),
	}
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
	// Both resource fetches ride on the same switch, and nil leaves the
	// feature they feed out of the view: one host that wants no extra
	// commands wants none of them. See `host_metrics` in the config format.
	if sel.HostMetrics {
		// The machine's own readings come off /proc and /sys and owe docker
		// nothing, so they survive every finding the probe can make.
		backend.Host = func() (host.Metrics, error) {
			return operator.HostMetrics(ctx)
		}
		backend.Processes = func() (host.ProcessSample, error) {
			return operator.HostProcesses(ctx)
		}
		backend.GPUs = func() ([]host.GPU, error) {
			return operator.GPUs(ctx)
		}
		// What the daemon is holding is a daemon question, not a compose one:
		// a host with a working docker and a compose this cannot drive still
		// has images, volumes and build cache worth showing.
		if capabilities.CanReachDaemon() {
			backend.DiskUsage = func() ([]compose.DiskUsage, error) {
				return operator.DiskUsage(ctx)
			}
		}
		// The container readings are addressed by container, so they have
		// nothing to ask about where there is no service list.
		if capabilities.CanCompose() {
			backend.Stats = func(services []compose.Service) (compose.CgroupSample, error) {
				pids := make([]int, 0, len(services))
				for _, service := range services {
					if service.Pid > 0 {
						pids = append(pids, service.Pid)
					}
				}
				return operator.ContainerCgroups(ctx, pids)
			}
			backend.LiveStats = func() (operations.Feed, error) {
				return operator.FollowStats(ctx)
			}
		}
	}
	return tui.Run(info, backend)
}

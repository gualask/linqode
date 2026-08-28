package main

import (
	"context"
	"fmt"
	"io"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/remote"
	"github.com/gualask/linqode/internal/tui"
)

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
	target, err := remote.Resolve(sel.Spec)
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "Connecting to %s@%s:%d ...\n",
		target.User, target.DisplayHost, target.Port)
	session, err := remote.Connect(ctx, target, terminalPrompter{})
	if err != nil {
		return err
	}
	defer session.Close()

	info := tui.Info{
		Target:     target.User + "@" + target.DisplayHost,
		ComposeDir: sel.ComposeDir,
		Scripts:    sel.Scripts,
	}
	operator := operations.NewHostOperator(session, sel.ComposeDir, sel.Scripts...)
	backend := tui.Backend{
		Services: func() ([]compose.Service, error) {
			return operator.Status(ctx)
		},
		Logs: func(service string, tail int) (operations.Feed, error) {
			return operator.Logs(ctx, service, tail, true)
		},
		ActionPreview: operator.ActionPreview,
		Action: func(action operations.ServiceAction, service string) (operations.Feed, error) {
			return operator.Action(ctx, action, service)
		},
		Script: func(name string) (operations.Feed, error) {
			return operator.Script(ctx, name)
		},
		AdHoc: func(command string) (operations.Feed, error) {
			return operator.AdHoc(ctx, command)
		},
	}
	// Both resource fetches ride on the same switch, and nil leaves the
	// feature they feed out of the view: one host that wants no extra
	// commands wants none of them. See `host_metrics` in the config format.
	if sel.HostMetrics {
		backend.Host = func() (host.Metrics, error) {
			return operator.HostMetrics(ctx)
		}
		backend.Stats = func() ([]compose.ContainerStats, error) {
			return operator.ContainerStats(ctx)
		}
		backend.LiveStats = func() (operations.Feed, error) {
			return operator.FollowStats(ctx)
		}
	}
	return tui.Run(info, backend)
}

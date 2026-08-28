package main

import (
	"context"
	"fmt"
	"io"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/remote"
)

type machineConnector func(
	context.Context,
	operations.ConfiguredHost,
) (cli.SafeOperator, func(), error)

func runMachine(
	ctx context.Context,
	invocation cli.Invocation,
	catalog operations.Catalog,
	connect machineConnector,
	stdout io.Writer,
	stderr io.Writer,
) int {
	switch invocation.Command {
	case cli.CommandHosts, cli.CommandScripts:
		return cli.Run(invocation, catalog, stdout, stderr)
	case cli.CommandStatus, cli.CommandStats, cli.CommandLogs,
		cli.CommandRestart, cli.CommandStop, cli.CommandStart, cli.CommandScript:
		return runConnectedCommand(ctx, invocation, catalog, connect, stdout, stderr)
	default:
		return cli.Run(invocation, catalog, stdout, stderr)
	}
}

func runConnectedCommand(
	ctx context.Context,
	invocation cli.Invocation,
	catalog operations.Catalog,
	connect machineConnector,
	stdout io.Writer,
	stderr io.Writer,
) int {
	configured, err := catalog.SelectHost(invocation.Host)
	if err != nil {
		return cli.Report(stderr, cli.SelectionFailure(invocation.Operation(), err))
	}
	if invocation.Command == cli.CommandScript {
		if err := configured.RequireScript(invocation.ScriptName); err != nil {
			return cli.Report(stderr, cli.SelectionFailure(invocation.Operation(), err))
		}
	}
	if failure := cli.ReadPreflight(invocation, configured.HostMetrics); failure != nil {
		return cli.Report(stderr, failure)
	}
	operator, closeConnection, err := connect(ctx, configured)
	if err != nil {
		return cli.Report(stderr, cli.ConnectionFailure(invocation.Operation(), err))
	}
	if closeConnection != nil {
		defer closeConnection()
	}
	if isMutation(invocation.Command) {
		return cli.RunMutation(ctx, invocation, operator, stdout, stderr)
	}
	return cli.RunRead(ctx, invocation, operator, stdout, stderr)
}

func isMutation(command cli.Command) bool {
	switch command {
	case cli.CommandRestart, cli.CommandStop, cli.CommandStart, cli.CommandScript:
		return true
	default:
		return false
	}
}

func connectMachine(ctx context.Context, configured operations.ConfiguredHost) (cli.SafeOperator, func(), error) {
	target, err := remote.Resolve(configured.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot resolve configured host %q: %w", configured.Name, err)
	}
	session, err := remote.Connect(ctx, target, cli.NonInteractivePrompter{})
	if err != nil {
		return nil, nil, err
	}
	return operations.NewConfiguredHostOperator(session, configured), session.Close, nil
}

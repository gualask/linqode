// Command linqode provides an SSH TUI for humans and a constrained JSON
// command interface for automation.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/operations"
)

// shutdownSignals end a run by cancelling its context rather than by killing
// the process, so that teardown runs: a local command lives in a process
// group of its own, which neither the terminal's SIGINT nor its SIGHUP
// reaches, and only the session closing it stands between it and outliving
// Linqode. SIGHUP is the terminal going away, SIGTERM a polite kill. Every
// one of them exists on Windows too, where the last two are never sent.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

func main() {
	os.Exit(run())
}

// run is main with its deferred cleanup intact: os.Exit runs no defers, so
// it is called only once everything here has returned.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()
	return execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

// execute is the process composition root. Parsing and machine presentation
// belong to cli; configured capability discovery belongs to operations.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	invocation, failure := cli.Parse(args)
	if failure != nil {
		return cli.Report(stderr, failure)
	}

	switch invocation.Command {
	case cli.CommandHelp:
		cli.PrintUsage(stdout, invocation.HelpFor)
		return 0
	case cli.CommandTUI:
		if err := runTUI(ctx, invocation.ConfigPath, invocation.Host, stderr); err != nil {
			fmt.Fprintf(stderr, "linqode: %v\n", err)
			return 1
		}
		return 0
	}

	cfg, err := config.Load("")
	if err != nil {
		return cli.Report(stderr,
			cli.OperationalFailure(invocation.Operation(), "config_error", err))
	}
	return runMachine(ctx, invocation, operations.NewCatalog(cfg), connectMachine, stdout, stderr)
}

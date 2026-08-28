// Command linqode provides an SSH TUI for humans and a constrained JSON
// command interface for automation.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/operations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
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

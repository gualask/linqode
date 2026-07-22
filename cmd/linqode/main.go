// Command linqode is an SSH TUI for monitoring and operating remote servers.
// During the Go port it connects and runs one-shot commands (--exec); the
// compose views land with G2.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/remote"
)

func main() {
	configPath := flag.String("config", "", "config file (default ~/.config/linqode/config.toml)")
	execCommand := flag.String("exec", "", "run one command on the host and print its raw output")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: linqode [flags] [host]\n\n"+
			"host is a name from the config file or an inline [user@]host[:port];\n"+
			"it can be omitted with exactly one configured host.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}

	// Ctrl-C cancels the context, which terminates any running remote
	// command before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	code, err := run(ctx, *configPath, flag.Arg(0), *execCommand)
	if err != nil {
		fmt.Fprintf(os.Stderr, "linqode: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run connects and either runs one --exec command (returning its exit code)
// or, for now, just reports the successful connection.
func run(ctx context.Context, configPath, hostArg, execCommand string) (int, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return 0, err
	}
	sel, err := cfg.Select(hostArg)
	if err != nil {
		return 0, err
	}
	target, err := remote.Resolve(sel.Spec)
	if err != nil {
		return 0, err
	}

	session, err := remote.Connect(ctx, target, terminalPrompter{})
	if err != nil {
		return 0, err
	}
	defer session.Close()

	if execCommand == "" {
		fmt.Printf("connected to %s@%s:%d — the compose views land with G2\n",
			target.User, target.DisplayHost, target.Port)
		return 0, nil
	}

	events, err := session.ExecStream(ctx, execCommand)
	if err != nil {
		return 0, err
	}
	exitCode := 0
	for ev := range events {
		switch ev.Kind {
		case remote.ExecStdout:
			os.Stdout.Write(ev.Data)
		case remote.ExecStderr:
			os.Stderr.Write(ev.Data)
		case remote.ExecExit:
			exitCode = ev.ExitCode
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("interrupted: %w", err)
	}
	return exitCode, nil
}

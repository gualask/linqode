// Command linqode is an SSH TUI for monitoring and operating remote
// servers: the compose status view by default, or one-shot commands with
// --exec.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/remote"
	"github.com/gualask/linqode/internal/tui"
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

	fmt.Fprintf(os.Stderr, "Connecting to %s@%s:%d ...\n",
		target.User, target.DisplayHost, target.Port)
	session, err := remote.Connect(ctx, target, terminalPrompter{})
	if err != nil {
		return 0, err
	}
	defer session.Close()

	if execCommand == "" {
		info := tui.Info{
			Target:     target.User + "@" + target.DisplayHost,
			ComposeDir: sel.ComposeDir,
		}
		psCommand := compose.PsCommand(sel.ComposeDir)
		return 0, tui.Run(info, func() ([]compose.Service, error) {
			return fetchServices(ctx, session, psCommand)
		})
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

// fetchServices runs `docker compose ps` remotely and parses its output.
func fetchServices(ctx context.Context, session *remote.Session, command string) ([]compose.Service, error) {
	out, err := session.Exec(ctx, command)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, errors.New(execFailure(out))
	}
	return compose.ParsePS(out.Stdout)
}

// execFailure gives a one-line reason for a failed remote command: the last
// stderr line when there is one (compose puts the actual error there), the
// exit code otherwise.
func execFailure(out remote.ExecOutput) string {
	lines := strings.Split(strings.TrimRight(string(out.Stderr), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	if out.ExitCode >= 0 {
		return fmt.Sprintf("remote command failed with exit code %d", out.ExitCode)
	}
	return "remote command failed without an exit status"
}

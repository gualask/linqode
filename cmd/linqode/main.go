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
	"github.com/gualask/linqode/internal/logs"
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
		for _, script := range sel.Scripts {
			info.Scripts = append(info.Scripts, tui.Script(script))
		}
		psCommand := compose.PsCommand(sel.ComposeDir)
		fetch := func() ([]compose.Service, error) {
			return fetchServices(ctx, session, psCommand)
		}
		exec := func(command string) (tui.LogFeed, error) {
			return startLogFeed(ctx, session, command)
		}
		return 0, tui.Run(info, fetch, exec)
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

// startLogFeed starts a remote follower and pumps its byte chunks into
// complete line events for the TUI. Stopping the feed cancels the remote
// command.
func startLogFeed(ctx context.Context, session *remote.Session, command string) (tui.LogFeed, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	events, err := session.ExecStream(streamCtx, command)
	if err != nil {
		cancel()
		return tui.LogFeed{}, err
	}

	out := make(chan tui.LogEvent, 4096)
	go func() {
		defer close(out)
		send := func(ev tui.LogEvent) bool {
			select {
			case out <- ev:
				return true
			case <-streamCtx.Done():
				return false // the feed was stopped and nobody drains it
			}
		}
		var stdout, stderr logs.LineAssembler
		exitCode := -1
		for ev := range events {
			switch ev.Kind {
			case remote.ExecStdout:
				for _, line := range stdout.Push(ev.Data) {
					if !send(tui.LogEvent{Kind: tui.LogLine, Text: line}) {
						return
					}
				}
			case remote.ExecStderr:
				for _, line := range stderr.Push(ev.Data) {
					if !send(tui.LogEvent{Kind: tui.LogStderrLine, Text: line}) {
						return
					}
				}
			case remote.ExecExit:
				exitCode = ev.ExitCode
			}
		}
		if line, ok := stdout.Finish(); ok && !send(tui.LogEvent{Kind: tui.LogLine, Text: line}) {
			return
		}
		if line, ok := stderr.Finish(); ok && !send(tui.LogEvent{Kind: tui.LogStderrLine, Text: line}) {
			return
		}
		send(tui.LogEvent{Kind: tui.LogEnded, ExitCode: exitCode})
	}()
	return tui.LogFeed{Events: out, Stop: cancel}, nil
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

// Package cli implements Linqode's machine-facing command adapter.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gualask/linqode/internal/operations"
)

type Command string

const (
	CommandTUI     Command = "tui"
	CommandHelp    Command = "help"
	CommandHosts   Command = "hosts"
	CommandScripts Command = "scripts"
	CommandStatus  Command = "status"
	CommandStats   Command = "stats"
	CommandLogs    Command = "logs"
	CommandRestart Command = "restart"
	CommandStop    Command = "stop"
	CommandStart   Command = "start"
	CommandScript  Command = "script"
)

var machineCommands = map[string]Command{
	"hosts":   CommandHosts,
	"scripts": CommandScripts,
	"status":  CommandStatus,
	"stats":   CommandStats,
	"logs":    CommandLogs,
	"restart": CommandRestart,
	"stop":    CommandStop,
	"start":   CommandStart,
	"script":  CommandScript,
}

// Invocation is the presentation-neutral result of command-line parsing.
type Invocation struct {
	Command    Command
	ConfigPath string
	Host       string
	Service    string
	ScriptName string
	Tail       int
	Follow     bool
	HelpFor    Command
}

func (i Invocation) Operation() string {
	if i.Command == CommandHelp {
		return string(i.HelpFor)
	}
	return string(i.Command)
}

// Parse routes args to the human TUI or a reserved machine command. Global
// --config is accepted only when the selected route is the TUI.
func Parse(args []string) (Invocation, *Failure) {
	global := newFlagSet("linqode")
	configPath := global.String("config", "", "config file")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Invocation{Command: CommandHelp}, nil
		}
		return Invocation{}, inputFailure("command", "invalid_option", err.Error())
	}

	rest := global.Args()
	if len(rest) == 0 {
		return Invocation{Command: CommandTUI, ConfigPath: *configPath}, nil
	}

	name, tail := rest[0], rest[1:]
	if name == "tui" {
		return parseTUI(tail, *configPath)
	}
	if name == "help" {
		return parseHelp(tail)
	}
	if command, ok := machineCommands[name]; ok {
		if flagWasSet(global, "config") {
			return Invocation{}, inputFailure(name, "invalid_option", "--config is available only for the TUI")
		}
		return parseMachine(command, tail)
	}
	if len(rest) != 1 {
		return Invocation{}, inputFailure("tui", "usage", "the TUI accepts at most one host")
	}
	return Invocation{Command: CommandTUI, ConfigPath: *configPath, Host: name}, nil
}

func parseTUI(args []string, inheritedConfig string) (Invocation, *Failure) {
	flags := newFlagSet("tui")
	configPath := flags.String("config", inheritedConfig, "config file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Invocation{Command: CommandHelp, HelpFor: CommandTUI}, nil
		}
		return Invocation{}, inputFailure("tui", "invalid_option", err.Error())
	}
	if flags.NArg() > 1 {
		return Invocation{}, inputFailure("tui", "usage", "the TUI accepts at most one host")
	}
	return Invocation{Command: CommandTUI, ConfigPath: *configPath, Host: flags.Arg(0)}, nil
}

func parseHelp(args []string) (Invocation, *Failure) {
	if len(args) == 0 {
		return Invocation{Command: CommandHelp}, nil
	}
	if len(args) != 1 {
		return Invocation{}, inputFailure("help", "usage", "help accepts at most one command")
	}
	target, ok := commandByName(args[0])
	if !ok {
		return Invocation{}, inputFailure("help", "invalid_argument", fmt.Sprintf("unknown command %q", args[0]))
	}
	return Invocation{Command: CommandHelp, HelpFor: target}, nil
}

func parseMachine(command Command, args []string) (Invocation, *Failure) {
	if hasConfigOption(args) {
		return Invocation{}, inputFailure(string(command), "invalid_option", "--config is available only for the TUI")
	}
	if slices.Contains(args, "-h") || slices.Contains(args, "--help") {
		return Invocation{Command: CommandHelp, HelpFor: command}, nil
	}

	switch command {
	case CommandHosts:
		return parseOperands(command, args, 0)
	case CommandScripts:
		return parseOperands(command, args, 1)
	case CommandStatus:
		return parseOperands(command, args, 1)
	case CommandStats:
		return parseStats(args)
	case CommandLogs:
		return parseLogs(args)
	case CommandRestart, CommandStop, CommandStart:
		return parseMutationOperands(command, args, false)
	case CommandScript:
		return parseMutationOperands(command, args, true)
	default:
		return Invocation{}, inputFailure(string(command), "invalid_argument", "unknown machine command")
	}
}

func parseMutationOperands(command Command, args []string, script bool) (Invocation, *Failure) {
	flags := newFlagSet(string(command))
	if failure := parseFlags(flags, command, args, 2); failure != nil {
		return Invocation{}, failure
	}
	invocation := Invocation{Command: command, Host: flags.Arg(0)}
	if script {
		invocation.ScriptName = flags.Arg(1)
	} else {
		invocation.Service = flags.Arg(1)
	}
	return invocation, nil
}

func parseStats(args []string) (Invocation, *Failure) {
	flags := newFlagSet("stats")
	follow := flags.Bool("follow", false, "follow container stats")
	if failure := parseFlags(flags, CommandStats, args, 1); failure != nil {
		return Invocation{}, failure
	}
	return Invocation{
		Command: CommandStats,
		Host:    flags.Arg(0),
		Follow:  *follow,
	}, nil
}

func parseLogs(args []string) (Invocation, *Failure) {
	flags := newFlagSet("logs")
	tail := flags.Int("tail", 200, "number of recent lines")
	follow := flags.Bool("follow", false, "follow new log lines")
	if failure := parseFlags(flags, CommandLogs, args, 2); failure != nil {
		return Invocation{}, failure
	}
	if *tail < 1 || *tail > operations.MaxLogTail {
		return Invocation{}, inputFailure("logs", "invalid_argument",
			fmt.Sprintf("log tail must be between 1 and %d, got %d", operations.MaxLogTail, *tail))
	}
	return Invocation{
		Command: CommandLogs,
		Host:    flags.Arg(0),
		Service: flags.Arg(1),
		Tail:    *tail,
		Follow:  *follow,
	}, nil
}

func parseOperands(command Command, args []string, count int) (Invocation, *Failure) {
	flags := newFlagSet(string(command))
	if failure := parseFlags(flags, command, args, count); failure != nil {
		return Invocation{}, failure
	}
	invocation := Invocation{Command: command}
	if count > 0 {
		invocation.Host = flags.Arg(0)
	}
	return invocation, nil
}

func parseFlags(flags *flag.FlagSet, command Command, args []string, operands int) *Failure {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return inputFailure(string(command), "invalid_option", err.Error())
	}
	if flags.NArg() != operands {
		return inputFailure(string(command), "usage",
			fmt.Sprintf("%s expects %d argument(s)", command, operands))
	}
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	return flags
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == name {
			set = true
		}
	})
	return set
}

func hasConfigOption(args []string) bool {
	for _, arg := range args {
		if arg == "-config" || arg == "--config" ||
			strings.HasPrefix(arg, "-config=") || strings.HasPrefix(arg, "--config=") {
			return true
		}
	}
	return false
}

func commandByName(name string) (Command, bool) {
	if name == "tui" {
		return CommandTUI, true
	}
	command, ok := machineCommands[name]
	return command, ok
}

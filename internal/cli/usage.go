package cli

import (
	"fmt"
	"io"
)

// PrintUsage writes conventional human-readable help. Machine operation
// results and invalid invocations use JSON instead.
func PrintUsage(w io.Writer, command Command) {
	switch command {
	case CommandTUI:
		fmt.Fprintln(w, "usage: linqode tui [--config path] [host]")
	case CommandHosts:
		fmt.Fprintln(w, "usage: linqode hosts")
	case CommandScripts:
		fmt.Fprintln(w, "usage: linqode scripts <host>")
	case CommandStatus:
		fmt.Fprintln(w, "usage: linqode status <host>")
	case CommandStats:
		fmt.Fprintln(w, "usage: linqode stats [--follow] <host>")
	case CommandLogs:
		fmt.Fprintln(w, "usage: linqode logs [--tail N] [--follow] <host> <service>")
	case CommandRestart, CommandStop, CommandStart:
		fmt.Fprintf(w, "usage: linqode %s <host> <service>\n", command)
	case CommandScript:
		fmt.Fprintln(w, "usage: linqode script <host> <name>")
	default:
		fmt.Fprintln(w, `usage: linqode [--config path] [host]
       linqode tui [--config path] [host]
       linqode hosts
       linqode scripts <host>
       linqode status <host>
       linqode stats [--follow] <host>
       linqode logs [--tail N] [--follow] <host> <service>
       linqode restart|stop|start <host> <service>
       linqode script <host> <name>

host is a name from the config file or, for the TUI only, an inline
[user@]host[:port]. With exactly one configured host it may be omitted.`)
	}
}

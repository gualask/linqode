# Linqode

An SSH terminal UI and constrained machine interface for Docker Compose.
Humans can inspect and operate a remote project in the TUI; automation can use
typed JSON commands without receiving arbitrary SSH execution. Agentless:
nothing to install on the server.

## Getting started

Download the archive for your platform from
[GitHub Releases](https://github.com/gualask/linqode/releases), extract it, and
put the `linqode` binary somewhere on your `PATH`. Alternatively, with Go
installed:

```bash
go install github.com/gualask/linqode/cmd/linqode@latest
```

To build from a local checkout, run `go build -o linqode ./cmd/linqode`.

The TUI reuses your existing SSH setup: keys from the agent or `~/.ssh`,
aliases from `~/.ssh/config`, host verification against `~/.ssh/known_hosts`
(unknown hosts prompt for confirmation, like OpenSSH). If `ssh user@host`
works, Linqode works.

```bash
linqode deploy@203.0.113.10        # inline host, no config needed
linqode myapp                      # a host from the config file
linqode                            # with a single configured host
linqode local                      # this machine, no SSH in the middle
linqode tui status                 # explicit TUI when a host has a command name
```

## This machine

`local` is a target like any other: the same screen, watching the machine
Linqode runs on. Nothing is installed and nothing is connected to — commands
run here, under `sh -c`, in your home directory and with your environment.

It is meant for the machine part of the dashboard rather than for Compose: on
your own laptop `docker compose ps` is already at hand, while the meters, the
filesystems, the process table, the temperatures and the graphics card are
not. It works on Linux and macOS; Windows is out of scope.

On macOS the readings are taken natively, because there is no `/proc` to read
and no command that makes up for it. What that costs, and what a Mac reports
that a Linux host does not, is in [docs/monitoring.md](docs/monitoring.md).

Two things to know. If you have `DOCKER_HOST` exported or a docker context
selected, this session drives *that* daemon, not a local one — the header says which,
in yellow, whenever it is not the plain socket. And the machine interface
below never accepts a local host: an agent on this machine already has a
shell, so Linqode grants it nothing, and `linqode hosts` does not list one.

## Configuration

`~/.config/linqode/config.toml` (the TUI can override it with `--config`):

```toml
[hosts.myapp]
host = "deploy@203.0.113.10"   # an ~/.ssh/config alias, or "local"
compose_dir = "/srv/myapp"     # where compose.yaml lives on the server
host_metrics = true            # optional: resource collection, on by default

[hosts.myapp.scripts]          # optional server commands allowed by name
disk = "df -h"
mem = "free -m"

[hosts.laptop]                 # `linqode laptop` watches this machine
host = "local"
compose_dir = "~/Dev/myapp"
```

`host = "local"` gives the local target a project and scripts of its own.
`localhost` is not the same thing and is not taken over: it means what it
means to `ssh`, a connection through sshd. A `[hosts.local]` entry of your own
wins over the keyword.

`--config <path>` is available only to the TUI. Machine commands always use
the default file above so their authorized hosts and scripts come from one
operator-controlled location.

## Machine interface

The machine interface is intended for agents and other automation. It accepts
only configured host names, typed Compose operations, and exact scripts from
the selected host's `scripts` table:

| Command | Result |
| --- | --- |
| `linqode hosts` | configured host names; no SSH connection |
| `linqode scripts <host>` | configured script names; no SSH connection |
| `linqode status <host>` | current Compose services |
| `linqode stats <host>` | one host/container resource snapshot |
| `linqode stats --follow <host>` | resource event stream |
| `linqode logs [--tail N] [--follow] <host> <service>` | bounded logs by default, or an explicit follow stream |
| `linqode restart <host> <service>` | restart one validated service |
| `linqode stop <host> <service>` | stop one validated service |
| `linqode start <host> <service>` | start one validated service |
| `linqode script <host> <name>` | one exact configured server command |

One-shot results are JSON; streams are JSON Lines. Remote stdout and stderr
are typed events on stdout, followed by an `exit` event when the server
supplies a status. Linqode diagnostics are JSON on stderr. Exit codes are `0`
for success, `2` for invalid input or an unknown configured name, `1` for a
Linqode/configuration/transport failure, and `130` for local interruption.
Lifecycle actions and scripts instead propagate a reported non-zero remote
exit code exactly. Logs default to 200 lines; `--tail` accepts values from 1
through 10,000.

Connecting establishes what the host can be asked for, once. A command that
needs Compose on a host that cannot run it is refused before it runs, with the
condition named — `docker_permission_denied` (the daemon is there and this
account may not reach it), `docker_unavailable`, `compose_dir_missing`, or
`compose_unavailable` — instead of exiting with whatever the remote shell
printed. `script` is exempt: a configured command has never needed Docker. In
the TUI the same finding turns the Compose features off and leaves everything
else running, so a host without Docker still shows its meters, filesystems,
temperatures, processes and scripts.

Machine authentication does not prompt, accept an unknown host key, or ask for
a key passphrase. Prepare trust and credentials with OpenSSH or the TUI first.
Script arguments cannot be supplied at runtime: put every allowed variant in
the TOML as its own named command. This boundary assumes the agent cannot
modify the operator-controlled config or SSH files, and that the process
environment is the operator's: `HOME` selects which config and which SSH files
are used, `SSH_AUTH_SOCK` which agent.

## Keys

**Status view** — the project's services:

| Key | Action |
| --- | ------ |
| `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` | move the selection |
| `Tab` / `Shift+Tab` | move focus around the header, the table and the feed; focus starts on the header |
| `Enter` | open what has focus: logs, the system view, an event's container |
| `Esc` | back out of a detail, a menu, or the `!` prompt; never quits |
| `c` | act on the selected service: restart / stop / start — on the table and the feed, where there is one |
| `x` | run a predefined script |
| `!` | type a command to run on the host |
| `a` | live panel: a sample a second, with each container's CPU and memory over the last ten minutes |
| `r` | refresh everything now |
| `q` | quit, from wherever you are |

`c` and `x` open a menu — arrows to choose, `Enter` to run, `Esc` to back
out — that shows the exact command before it runs. No two keys in Linqode
differ only by the shift key, so a mistyped capital can never stop a service
you meant to start.

Scripts and `!` commands run in your login directory, where
`ssh user@host 'command'` would run them, not in `compose_dir`. Their output
streams into the log view, with the exit code when the command finishes.

The RESTARTS column counts how many times docker has restarted each
container: a count that climbs is a service crash-looping rather than
recovering, and `-` means the host did not report one. The table re-reads
itself when the daemon says something changed, so a container that dies is
on screen as soon as it dies.

**The host band** is the row of meters in the header box, whose title says
which host you are on and where its project lives. It shows CPU, memory, the
*fullest* filesystem (not always `/`: a comfortable root says nothing about
the `/var/lib/docker` that is about to fill), swap once a meaningful share of
it is in use, and a temperature where the hardware reports one.

The session opens with the header focused, so `Enter` goes straight into the
**system view**: per-core CPU, load, swap, every filesystem with its device,
network throughput, kernel pressure, temperatures and graphics cards; short
charts of CPU, memory and network over the last minutes, drawn as solid
columns;
and the processes, ranked by memory and by CPU side by side — on a narrow
terminal it is one list, and `s` switches its ranking.

**The events feed** under the table is what the daemon reported happening,
newest first: restarts, health changes, OOM kills, with the exit code read
rather than printed — 137 is a container that was killed. `Enter` on one
opens that container's logs.

Under the table, where it leaves room, is what docker holds on disk: images,
containers, volumes and build cache, with how much of each the daemon would
give back.

The per-container columns — CPU, MEM, NET RX/TX and IO R/W — are sampled
while the table is on screen. To watch resources move rather than glance at
them, `a` opens a live panel that streams a sample per second for as long as
it is open. Its strips reach back further than that: the sampled readings are
remembered too, so a memory leak is already a climb when the panel opens.

**Log view** — follow mode with scrollback:

| Key | Action |
| --- | ------ |
| `↑`/`↓`, `PgUp`/`PgDn` | move the cursor over the lines (leaves follow mode) |
| `Enter` | open the line under the cursor in full: whole message, one field per row |
| `l` or `End` / `Home` | back to live (follow) / jump to the top |
| `/` then `n`/`N` | search, next/previous match |
| `a` | open or close the stats panel: counts by level and by a field, recently and in all |
| `tab` | move the keys between the log and the stats panel |
| `↑`/`↓`, `Enter` (panel) | move over the counts; add the row to the filter, or take it out |
| `t` | count by the next field (the panel picks the first itself) |
| `f` | type a filter (`level=error route!=/healthz msg="a b"`) |
| `x` | reset: clear the filter and the search |
| `s` | toggle structured rendering, when detection gets it wrong |
| `Esc` | back to the status view (from the panel or an open line: back to the log) |
| `q` | quit |

While a search or a filter is being typed, every key types:
`Esc` cancels the input, and `q` is a `q`.

## Documentation

- [docs/PROJECT.md](docs/PROJECT.md) — vision, MVP scope, settled policies,
  roadmap
- [docs/architecture.md](docs/architecture.md) — the shape of the codebase:
  packages, dependency direction, how a session starts
- [docs/monitoring.md](docs/monitoring.md) — what is read off the host, on
  which cadence, and what each reading cost when it was measured
- [docs/interface.md](docs/interface.md) — the screen: panels, focus, the
  band, the system view, the feed
- [docs/operations.md](docs/operations.md) — compose lifecycle, scripts,
  ad-hoc commands, log following, the machine interface
- [docs/tests.md](docs/tests.md) — testing strategy, layers, conventions
- [docs/docker-setup.md](docs/docker-setup.md) — a Docker engine for the e2e
  fixture, and keeping it off when unused
- [docs/porting.md](docs/porting.md) — how the Go codebase was ported from
  the Rust reference implementation (tag `rust-mvp`)

## License

[ISC](LICENSE)

# Linqode

An SSH terminal UI and constrained machine interface for Docker Compose.
Humans can inspect and operate a remote project in the TUI; automation can use
typed JSON commands without receiving arbitrary SSH execution. Agentless:
nothing to install on the server.

> **Status:** MVP implemented (July 2026), not yet validated against real
> deployments.

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
linqode tui status                 # explicit TUI when a host has a command name
```

## Configuration

`~/.config/linqode/config.toml` (the TUI can override it with `--config`):

```toml
[hosts.myapp]
host = "deploy@203.0.113.10"   # or an ~/.ssh/config alias
compose_dir = "/srv/myapp"     # where compose.yaml lives on the server
host_metrics = true            # optional: resource collection, on by default

[hosts.myapp.scripts]          # optional server commands allowed by name
disk = "df -h"
mem = "free -m"
```

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

Machine authentication does not prompt, accept an unknown host key, or ask for
a key passphrase. Prepare trust and credentials with OpenSSH or the TUI first.
Script arguments cannot be supplied at runtime: put every allowed variant in
the TOML as its own named command. This boundary assumes the agent cannot
modify the operator-controlled config or SSH files.

## Keys

**Status view** — the project's services:

| Key | Action |
| --- | ------ |
| `j`/`k` | select service |
| `Enter` | follow the service's logs |
| `c` | act on the service: restart / stop / start |
| `x` | run a predefined script |
| `!` | type a command to run on the host |
| `a` | live panel: per-second CPU sparkline per container |
| `r` | refresh now (auto-refreshes every 5 s) |
| `q` | quit |

`c` and `x` open a menu — `j`/`k` to choose, `Enter` to run, `Esc` to back
out — that shows the exact command before it runs. Service actions sit
behind one deliberately: no two keys in Linqode differ only by the shift
key, so a mistyped capital can never stop a service you meant to start.

Scripts and `!` commands run where `ssh user@host 'command'` would run
them — your login directory, not `compose_dir`. Only the actions Linqode
builds itself (the `c` menu, log following) target the compose project.
Output streams into the log view, with the exit code when the command
finishes.

The RESTARTS column shows how many times docker has restarted each
container — a count that climbs is a service crash-looping rather than
recovering. It comes from a `docker inspect` alongside the refresh, and
reads `-` on a host where that command did not answer.

The machine's load, memory, disk and uptime go in a panel down the right
side — or, on a terminal too narrow for it, in a line under the header.
Either way they are sampled every 5 seconds, which costs about 2 ms.

The table's CPU and MEM columns are refreshed every 20 seconds instead: a
`docker stats` sample takes the daemon ~2 seconds to produce, whatever the
project's size, because it reads the cgroups twice to derive a percentage.
When you want to watch resources move rather than glance at them, `a` opens
the live panel, which streams a sample per second for as long as it is open
and stops the remote command when you close it.

**Log view** — follow mode with scrollback:

| Key | Action |
| --- | ------ |
| `j`/`k`, `PgUp`/`PgDn` | scroll (leaves follow mode) |
| `G` / `g` | jump to bottom (follow) / top |
| `/` then `n`/`N` | search, next/previous match |
| `f` | filter JSONL logs by field (`level=error app!=web`) |
| `s` | toggle structured rendering (auto-detected for JSONL) |
| `a` | toggle the live stats panel |
| `t` | choose the field the stats panel counts |
| `Esc` | back to the status view |

## Documentation

- [docs/PROJECT.md](docs/PROJECT.md) — vision, MVP scope, settled policies,
  roadmap
- [docs/architecture.md](docs/architecture.md) — how it works: components
  and flows
- [docs/tests.md](docs/tests.md) — testing strategy, layers, conventions
- [docs/porting.md](docs/porting.md) — how the Go codebase was ported from
  the Rust reference implementation (tag `rust-mvp`)

## License

[ISC](LICENSE)

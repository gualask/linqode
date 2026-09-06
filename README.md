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
modify the operator-controlled config or SSH files.

## Keys

**Status view** — the project's services:

| Key | Action |
| --- | ------ |
| `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` | move the selection |
| `Tab` / `Shift+Tab` | move focus around the header, the table and the feed; focus starts on the header |
| `Enter` | open what has focus: logs, the system view, an event's container |
| `Esc` | back out of a detail, a menu, or the `!` prompt; never quits |
| `c` | act on the service: restart / stop / start |
| `x` | run a predefined script |
| `!` | type a command to run on the host |
| `a` | live panel: per-second CPU sparkline per container |
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
network throughput, kernel pressure, temperatures, graphics cards, what
docker is holding in images and volumes, and the top processes — `s` switches
those between ranking by memory and by CPU.

**The events feed** under the table is what the daemon reported happening,
newest first: restarts, health changes, OOM kills, with the exit code read
rather than printed — 137 is a container that was killed. `Enter` on one
opens that container's logs.

The per-container columns — CPU, MEM, NET RX/TX and IO R/W — are sampled
while the table is on screen. To watch resources move rather than glance at
them, `a` opens a live panel that streams a sample per second for as long as
it is open.

**Log view** — follow mode with scrollback:

| Key | Action |
| --- | ------ |
| `↑`/`↓`, `PgUp`/`PgDn` | scroll (leaves follow mode) |
| `End` / `Home` | jump to bottom (follow) / top |
| `/` then `n`/`N` | search, next/previous match |
| `f` | filter JSONL logs by field (`level=error app!=web`) |
| `s` | toggle structured rendering (auto-detected for JSONL) |
| `a` | toggle the live stats panel |
| `t` | choose the field the stats panel counts |
| `Esc` | back to the status view |
| `q` | quit |

While a search, a filter or a field name is being typed, every key types:
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

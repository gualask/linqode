# Linqode

An SSH terminal UI for Docker Compose. Connect to a remote server the way
`ssh user@host` does, see the state of a compose project, follow and analyze
its logs — including real-time filtering and aggregation of JSONL logs — and
restart services or run predefined scripts. Agentless: nothing to install on
the server.

> **Status:** MVP implemented (July 2026), not yet validated against real
> deployments.

## Getting started

```bash
go build -o linqode ./cmd/linqode    # or: go install ./cmd/linqode
```

Linqode reuses your existing SSH setup: keys from the agent or `~/.ssh`,
aliases from `~/.ssh/config`, host verification against `~/.ssh/known_hosts`
(unknown hosts prompt for confirmation, like OpenSSH). If `ssh user@host`
works, Linqode works.

```bash
linqode deploy@203.0.113.10        # inline host, no config needed
linqode myapp                      # a host from the config file
linqode                            # with a single configured host
linqode myapp --exec "df -h"       # one-shot command, raw output
```

## Configuration

`~/.config/linqode/config.toml` (override with `--config`):

```toml
[hosts.myapp]
host = "deploy@203.0.113.10"   # or an ~/.ssh/config alias
compose_dir = "/srv/myapp"     # where compose.yaml lives on the server
host_metrics = true            # optional: header resource line, on by default

[hosts.myapp.scripts]          # optional commands runnable from the TUI
disk = "df -h"
mem = "free -m"
```

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

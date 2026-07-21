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
cargo build --release        # binary in target/release/linqode
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
| `R` / `s` / `S` | restart / stop / start the service |
| `x` | run a predefined script |
| `r` | refresh now (auto-refreshes every 5 s) |
| `q` | quit |

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

## License

[ISC](LICENSE)

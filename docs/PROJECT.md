# Linqode — Project Document

_Last updated: 2026-07-21_

## Vision

A single-binary TUI that runs on the operator's machine and connects to remote
servers over SSH to monitor and manage **Docker Compose** deployments. The tool
is **agentless**: nothing is installed on the server — Linqode drives the
standard `docker compose` CLI remotely and interprets its output locally.

The core differentiator is the **structured log engine**: existing Docker TUIs
(lazydocker, oxker, …) show logs as raw text; existing log analyzers (lnav,
Gonzo, …) know nothing about SSH or Docker. Linqode combines both: follow a
service's logs and, when they are JSONL, filter and aggregate them in real time
to surface problems immediately.

## MVP scope

1. **Connect** to a host over SSH, matching the behavior of a plain
   `ssh user@host` (see [Decided policies](#decided-policies)).
2. **Compose status view**: list services of a compose project with state,
   health, restarts (via `docker compose ps --format json` executed remotely).
3. **Log view** for a selected service (`docker compose logs -f`):
   - plain-text mode: tail-like follow, search;
   - structured mode (JSONL): parse each line, filter by field
     (e.g. `level=error`), live aggregations (counts by level/field over a
     time window).
4. **Run commands**: restart/stop/start a service; execute a predefined script
   or ad-hoc command on the host, with output captured in the TUI.

### Non-goals (for now)

- Kubernetes, Swarm, Podman.
- Multi-host dashboards / fleet management (single host per session first).
- A server-side agent or daemon of any kind.
- Metrics collection beyond what `docker compose ps` / `docker stats` provide.

## Architecture

```
┌─────────────────────────── laptop ───────────────────────────┐
│  TUI (ratatui)                                               │
│    │ app events / render state                               │
│  Core state machine (tokio tasks, channels)                  │
│    │                          │                              │
│  SSH session mgr (russh)    Log engine                       │
│    │  exec channels           │  line parser (text/JSONL)    │
│    │  streamed stdout/stderr  │  filters + windowed aggs     │
└────┼──────────────────────────┴──────────────────────────────┘
     │ SSH (port 22)
┌────▼───────────── server ────────────────┐
│  sshd → docker compose ps / logs / exec  │
└──────────────────────────────────────────┘
```

Key decisions:

- **Agentless over SSH**: the remote "API" is the `docker compose` CLI with
  `--format json` where available. No daemon, no socket forwarding required
  (socket forwarding of `/var/run/docker.sock` may come later as an option).
- **Async everywhere**: each SSH exec channel streams into the log engine through
  tokio channels; the TUI thread never blocks on network I/O.
- **The log engine is Docker-agnostic**: it consumes any line stream, so it can
  later be pointed at plain files (`tail -F` over SSH) without changes.

## Decided policies

These decisions are settled — do not re-litigate them when implementing:

- **Authentication (MVP)**: try the SSH agent first, then fall back to the
  default identity files in `~/.ssh` (`id_ed25519`, `id_rsa`, …), prompting for
  a passphrase in the TUI only if a key is encrypted. The goal is parity with a
  plain `ssh user@host` that "just works" once the public key is on the server.
  Password authentication is out of scope for the MVP.
- **Host key verification**: check against `~/.ssh/known_hosts`. Unknown host →
  show the fingerprint and ask for confirmation (trust-on-first-use, same UX as
  OpenSSH), then persist it. Key mismatch → refuse to connect with a clear
  error. Never skip verification.
- **Host/project selection**: a TOML config file (see below). The `host` value
  is either a `~/.ssh/config` alias or an inline `user@host[:port]`, so servers
  already reachable via plain `ssh` need no extra setup.

## Configuration

`~/.config/linqode/config.toml` (path overridable with `--config`):

```toml
[hosts.myapp]
host = "deploy@203.0.113.10"   # or an ssh_config alias like "myapp-prod"
compose_dir = "/srv/myapp"     # directory on the server containing compose.yaml

[hosts.myapp.scripts]          # optional predefined commands (M5)
disk = "df -h"
```

`linqode myapp` connects to that host and opens the compose project in
`compose_dir`. With a single configured host, plain `linqode` picks it.

## Testing strategy

- **Unit tests**: the SSH transport is behind a trait; the log engine and
  compose-output parsing are tested against captured fixtures (JSONL samples,
  `docker compose ps --format json` outputs) with no network involved.
- **Integration tests**: a `tests/fixture/` docker-compose in the repo runs a
  container with `sshd` + Docker (docker-in-docker) hosting a demo compose
  project that emits both plain-text and JSONL logs. Integration tests connect
  to it over real SSH and exercise the full path (connect → ps → logs → exec).
  This lets any developer — human or LLM — verify changes end-to-end locally
  without access to a real server.

## Technology choices

| Concern        | Crate                       | Rationale                                                            |
| -------------- | --------------------------- | -------------------------------------------------------------------- |
| TUI            | `ratatui` + `crossterm`     | De-facto standard, actively maintained, great docs                    |
| SSH            | `russh`                     | Pure-Rust async client, integrates with tokio, multiplexed channels   |
| Async runtime  | `tokio`                     | Required by russh; drives log streaming without blocking the UI      |
| Serialization  | `serde` + `serde_json`      | JSONL parsing, `docker compose ps --format json`                      |
| Config         | `toml` (via serde)          | Host definitions, saved filters, predefined scripts                   |
| SSH config     | `ssh2-config` (or similar)  | Reuse `~/.ssh/config` host aliases where possible                     |
| Errors         | `thiserror` + `anyhow`      | Library vs application error handling                                 |
| CLI entry      | `clap`                      | `linqode [host]` plus flags                                           |

Conventions carried over from the previous incarnation of the repo: Rust
edition 2024, cargo workspace with focused crates, ISC license.

### Proposed workspace layout

```
crates/
  linqode-ssh     # russh session management, exec channels, auth
  linqode-logs    # line stream parsing, JSONL filters, aggregations
  linqode-compose # docker compose command builders + output models
  linqode-tui     # ratatui app: views, keymaps, state
  linqode-cli     # binary entry point, config loading
```

## Roadmap

- **M1 — plumbing**: SSH connect + run a one-shot remote command, output in a
  minimal ratatui screen.
- **M2 — compose status**: parse `docker compose ps --format json`, service
  list view with refresh.
- **M3 — log follow**: streaming `logs -f` for a selected service, plain-text
  tail view with search.
- **M4 — structured logs**: JSONL detection, field filters, live aggregation
  panel (counts by level, top values of a field).
- **M5 — actions**: restart/stop/start service, predefined scripts from config.

## Prior art / references

Studied before the reboot (July 2026). None combines SSH + Compose awareness +
structured log analysis in one tool — that is the gap Linqode targets.

| Tool | What it is | Relevant for |
| ---- | ---------- | ------------ |
| [lazydocker](https://lazydocker.com/) ([GitHub](https://github.com/jesseduffield/lazydocker)) | De-facto standard Docker/Compose TUI (Go) | UX baseline: panels, keybindings, what users expect from a Docker TUI. Logs are raw text only; remote use requires installing it on the server or `DOCKER_HOST=ssh://` |
| [oxker](https://github.com/mrjackwills/oxker) | Docker TUI in Rust (ratatui) | Closest codebase to ours: reference for ratatui app structure, container list + log stream panels |
| [EasyDocker / DockMate](https://dev.to/janvandorth/every-docker-compose-tui-i-could-find-and-why-i-built-my-own-2oo0) | Recent compose-focused TUIs (Bubble Tea) | Survey article covers the landscape and why none satisfied the author — useful market map |
| [lnav](https://github.com/tstack/lnav) | Terminal log navigator (C++) | The most powerful structured-log UX: JSON awareness, SQL queries over logs, live filters. Reference for the log engine's feature set |
| [Gonzo](https://www.controltheory.com/gonzo/) ([GitHub](https://github.com/control-theory/gonzo)) | Real-time log analysis TUI (Go) | Reference for live aggregations on JSON streams (counts by severity, patterns). Pipe-based, no SSH/Docker awareness |
| [toolong](https://github.com/Textualize/toolong) | Log viewer/tailer with JSONL support (Python/Textual) | Simple, polished tail/merge/search UX for JSONL files |
| [fblog](https://github.com/brocode/fblog), [logshark](https://github.com/dpc/logshark), [json-log-viewer](https://github.com/Vedu1996/json-log-viewer) | Small JSON log viewers | Ideas for compact JSONL rendering and field selection |

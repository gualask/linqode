# Linqode — Project Document

_Last updated: 2026-07-21_

Vision, scope, settled decisions, and roadmap. How the system works is in
[architecture.md](architecture.md); testing in [tests.md](tests.md); user
setup (install, configuration, keys) in the top-level
[README](../README.md).

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
- **Host/project selection**: a TOML config file (format documented in the
  [README](../README.md)). The `host` value is either a `~/.ssh/config` alias
  or an inline `user@host[:port]`, so servers already reachable via plain
  `ssh` need no extra setup.

## Roadmap

- **M1 — plumbing** _(done, July 2026)_: SSH connect + run a one-shot remote
  command, output in a minimal ratatui screen (kept behind `--exec`).
- **M2 — compose status** _(done, July 2026)_: parse
  `docker compose ps --all --format json` (NDJSON and legacy array shapes),
  service table with state/health/ports coloring, selection, manual (`r`) and
  5-second auto refresh. Restart counts are not shown yet: `compose ps` does
  not report them (needs `docker inspect`, deferred).
- **M3 — log follow** _(done, July 2026)_: streaming exec with cancellation,
  line assembly, a bounded tail buffer, and a log view with follow mode,
  scrollback, and `/` search; Enter on a service opens it.
- **M4 — structured logs** _(done, July 2026)_: JSONL records with flattened
  field paths, `key=value` / `key!=value` filters, live aggregations (counts
  by level, top values of a chosen field), auto-detected structured
  rendering, and the stats side panel.
- **M5 — actions** _(done, July 2026)_: restart/stop/start the selected
  service; predefined scripts from the config, streamed into the shared
  follow view.

All MVP milestones are implemented. **None has been exercised against a real
server yet** — end-to-end validation waits for the `tests/fixture/` sshd +
docker-in-docker fixture (see [tests.md](tests.md)), then hardening against
real deployments.

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

# Linqode — Project Document

_Last updated: 2026-07-22_

Vision, scope, settled decisions, and roadmap. How the system works is in
[architecture.md](architecture.md); testing in [tests.md](tests.md); the Go
porting plan in [porting.md](porting.md); user setup (install, configuration,
keys) in the top-level [README](../README.md).

## Vision

A single-binary TUI that runs on the operator's machine and connects to remote
servers over SSH to **monitor and operate what runs there**: watch service
state, follow and analyze logs, and execute commands — Docker Compose
operations, plain Linux commands, or shell scripts. The tool is **agentless**:
nothing is installed on the server — Linqode drives standard CLIs remotely
over SSH and interprets their output locally.

**Docker Compose is the flagship integration** and the whole of the MVP scope
below, but the product direction is broader: a general remote-operations
cockpit where Compose awareness is one capability among monitoring and script
execution (see the post-parity roadmap).

A key differentiator is the **structured log engine**: existing Docker TUIs
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

- **Implementation language: Go** _(decided 2026-07-22)_. The product's core
  is SSH orchestration and remote execution, and there Go wins: a
  battle-tested SSH stack (`golang.org/x/crypto/ssh` + `knownhosts` + `agent`,
  ~15 years of production use vs the younger `russh`), goroutines/channels
  mapping naturally onto N concurrent remote streams with cancellation, the
  strongest prior art in this niche (lazydocker, Gonzo, Bubble Tea TUIs), and
  faster iteration. Rust's edge (JSONL parsing throughput) is not the
  bottleneck for this product. The Rust MVP (M1–M5) is kept as the **reference
  implementation** — tagged `rust-mvp`, then replaced in-tree by the Go port
  (see [porting.md](porting.md)).
- **Go stack**: Bubble Tea + Bubbles + Lipgloss (TUI),
  `golang.org/x/crypto/ssh` (SSH), `kevinburke/ssh_config` (`~/.ssh/config`
  aliases), `pelletier/go-toml/v2` (config), stdlib `encoding/json`, `flag`,
  and `errors`. Prefer these over alternatives unless a real blocker shows up.
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

### Rust MVP — reference implementation _(done, July 2026; tagged `rust-mvp`)_

- **M1 — plumbing**: SSH connect + run a one-shot remote command, output in a
  minimal ratatui screen (kept behind `--exec`).
- **M2 — compose status**: parse `docker compose ps --all --format json`
  (NDJSON and legacy array shapes), service table with state/health/ports
  coloring, selection, manual (`r`) and 5-second auto refresh. Restart counts
  are not shown yet: `compose ps` does not report them (needs
  `docker inspect`, deferred).
- **M3 — log follow**: streaming exec with cancellation, line assembly, a
  bounded tail buffer, and a log view with follow mode, scrollback, and `/`
  search; Enter on a service opens it.
- **M4 — structured logs**: JSONL records with flattened field paths,
  `key=value` / `key!=value` filters, live aggregations (counts by level, top
  values of a chosen field), auto-detected structured rendering, and the
  stats side panel.
- **M5 — actions**: restart/stop/start the selected service; predefined
  scripts from the config, streamed into the shared follow view.

All Rust milestones are implemented but **never exercised against a real
server**; that validation now happens on the Go port instead of being paid
twice.

### Go port _(current phase — plan in [porting.md](porting.md))_

The port is feature-driven, not a 1:1 translation: the Rust tree supplies
settled answers and test fixtures, but each feature is built the way Go
builds it best, and milestones may be reshaped when a simpler path shows
up. Each milestone leaves an offline `go test ./...` green:

- **G0 — scaffolding** _(done, July 2026)_: Go module, package layout, CI;
  config loading + host selection.
- **G1 — connect and remote exec** _(done, July 2026)_: connect like plain
  `ssh` (host-key TOFU, agent → identity-file auth), one-shot and streaming
  exec with cancellation; integration tests against a scripted loopback
  server.
- **G2 — compose status view** _(done, July 2026)_.
- **G3 — log following**.
- **G4 — structured log analysis**.
- **G5 — actions and scripts**. MVP feature set covered: the Rust tree is
  removed (still available at the `rust-mvp` tag).

### After parity

- **E2E fixture**: the `tests/fixture/` sshd + docker-in-docker compose
  fixture (see [tests.md](tests.md)) validating connect → ps → logs → exec
  against a live Docker, then hardening against real deployments.
- **Generic remote operations** (the broadened vision): ad-hoc command
  execution from the TUI, running `.sh` scripts with streamed output, PTY
  support for interactive commands (`sudo`, prompts), monitoring beyond
  Compose (`docker stats`, plain files via `tail -F`). To be scoped into
  milestones once parity and e2e validation land.

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

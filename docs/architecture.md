# Architecture

_Last updated: 2026-07-22_

> **Note:** this describes the **Rust reference implementation**. Linqode is
> being ported to Go (see [porting.md](porting.md)); the principles, flows,
> and policies below carry over unchanged and remain the spec for the port.
> This document is rewritten for the Go tree at parity (G5).

How Linqode works, at the level of components and flows. The vision, scope,
and settled policy decisions live in [PROJECT.md](PROJECT.md); testing is
documented in [tests.md](tests.md).

## Overview

Everything runs on the operator's machine. The remote "API" is the standard
`docker compose` CLI driven over SSH — no daemon, no agent, no socket
forwarding.

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

Two principles shape the design:

- **Async everywhere below the UI**: every remote operation streams through
  tokio channels; the TUI thread polls them from its synchronous event loop
  and never blocks on network I/O for rendering or input.
- **The log engine is Docker-agnostic**: it consumes a generic stream of
  bytes/lines, so it can later be pointed at plain files (`tail -F` over
  SSH) without changes.

## Workspace layout

| Crate | Responsibility |
| ----- | -------------- |
| `linqode-ssh` | russh session management: connect, host-key policy, auth, one-shot and streaming exec with cancellation |
| `linqode-compose` | `docker compose` command builders (with shell quoting) and output parsing into typed models |
| `linqode-logs` | log engine: line assembly, tail buffer, JSONL records, field filters, live aggregations, search |
| `linqode-tui` | ratatui application: views, keymaps, state |
| `linqode-cli` | binary entry point: CLI args, config loading, wiring session ↔ TUI |

| Concern | Crate | Rationale |
| ------- | ----- | --------- |
| TUI | `ratatui` + `crossterm` | De-facto standard, actively maintained |
| SSH | `russh` | Pure-Rust async client, tokio-native, multiplexed channels |
| Async runtime | `tokio` | Required by russh; drives streaming without blocking the UI |
| Serialization | `serde` + `serde_json` | JSONL parsing, `compose ps --format json` |
| Config | `toml` (via serde) | Host definitions, scripts |
| Errors | `thiserror` + `anyhow` | Library vs application error handling |
| CLI entry | `clap` | `linqode [host]` plus flags |

## Flows

### Startup and host selection

The CLI argument is either a name from the config file (which contributes
`compose_dir` and scripts) or an inline `[user@]host[:port]` spec; with a
single configured host, no argument is needed. The spec is then resolved
against `~/.ssh/config` (aliases, user, port), so hosts reachable by plain
`ssh` need no extra setup. With `--exec` the TUI shows one command's raw
output instead of the compose views.

### Connecting

One SSH session is established per run; every later operation opens its own
exec channel over it.

- **Host key**: checked against `~/.ssh/known_hosts`. Unknown host → show
  the fingerprint, ask for confirmation, persist on accept
  (trust-on-first-use, same UX as OpenSSH). Key mismatch → refuse with a
  clear error, never bypassable.
- **Auth**: SSH agent first, then the default identity files in `~/.ssh`,
  prompting for a passphrase only if a key is encrypted. Password auth is
  out of scope for the MVP.

### Compose status

The status view runs `docker compose ps --all --format json` in the
project's directory — a one-shot exec per refresh, triggered manually or by
a 5-second timer. Output is parsed into typed service rows (both the NDJSON
and the legacy array shape of `--format json` are accepted) and rendered as
a table with state/health coloring. Selection is preserved across
refreshes; a failed refresh shows the error while the last good table stays
on screen.

### Following logs

Opening a service starts `docker compose logs --follow` (no prefix, no
color, tailing recent history) on a streaming exec channel. From there:

1. stdout arrives as byte chunks; a line assembler reassembles complete
   lines across arbitrary chunk boundaries (stderr is tracked separately as
   diagnostics).
2. lines flow over a channel into the log engine's per-stream store: a
   bounded tail buffer (oldest lines drop when full) that also parses and
   indexes each line on entry.
3. the TUI drains pending lines once per tick — with an upper bound, so a
   log burst cannot starve input handling — and renders the visible slice.

The view follows the tail until the user scrolls up; jumping to the bottom
re-enters follow mode. Search runs over the buffered lines with wrap-around.
Closing the view cancels the remote command (terminate signal, then channel
close) and tears down the pipeline.

### Structured logs

Every incoming line is offered to the JSONL parser; a line that is a JSON
object becomes a record whose nested fields are flattened to dotted paths
(`http.status=500`). On top of these records:

- **Detection**: when most buffered lines parse as records, the view
  switches to structured rendering — timestamp, colored level, message,
  then remaining fields — with a manual override either way. Well-known
  level/message/timestamp key variants are recognized.
- **Filters**: `key=value` / `key!=value` terms (AND-ed,
  case-insensitive) narrow the visible view to matching records;
  plain-text lines are hidden while a filter is active. Scroll, search,
  and follow all operate on the filtered view.
- **Aggregations**: counts by level and top values of one user-chosen
  field, computed over the whole tail buffer and updated incrementally as
  lines enter and leave it; shown in a side panel.

### Service actions and scripts

Restart/stop/start on the selected service build the corresponding
`docker compose` command; predefined scripts from the config run verbatim
on the host (no compose-dir `cd`). Both reuse the log-follow pipeline: the
command's output streams into the same view, the exit code is shown on
completion, and the status view refreshes on return — so a restart's
effect is visible immediately.

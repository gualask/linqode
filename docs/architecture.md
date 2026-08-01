# Architecture

_Last updated: 2026-08-01_

How Linqode works, at the level of components and flows. The vision, scope,
and settled policy decisions live in [PROJECT.md](PROJECT.md); testing is
documented in [tests.md](tests.md); how the Go codebase came to be (and the
Rust reference it was ported from, tag `rust-mvp`) in
[porting.md](porting.md).

## Overview

Everything runs on the operator's machine. The remote "API" is the standard
`docker compose` CLI driven over SSH — no daemon, no agent, no socket
forwarding.

```
┌─────────────────────────── laptop ───────────────────────────┐
│  TUI (Bubble Tea)                                            │
│    │ messages / commands                                     │
│  App model: status view ⇄ log view                           │
│    │                          │                              │
│  SSH session (x/crypto/ssh)  Log engine (internal/logs)      │
│    │  exec channels           │  line assembly, tail buffer  │
│    │  streamed stdout/stderr  │  JSONL records, filters,     │
│    │                          │  search, stats               │
└────┼──────────────────────────┴──────────────────────────────┘
     │ SSH (port 22)
┌────▼───────────── server ────────────────┐
│  sshd → docker compose ps / logs / exec  │
└──────────────────────────────────────────┘
```

Two principles shape the design:

- **The UI loop never blocks.** Every SSH round-trip runs inside a Bubble
  Tea command (a background goroutine) whose outcome comes back as a
  message; streamed output flows through channels drained on a bounded
  tick. Rendering and input handling never wait on the network.
- **The log engine is Docker-agnostic**: it consumes generic streams of
  bytes and lines, so it can later be pointed at plain files (`tail -F`
  over SSH) or any other remote command without changes.

## Package layout

| Package | Responsibility |
| ------- | -------------- |
| `cmd/linqode` | entry point: flags, terminal prompts, wiring session ⇄ TUI, the feed pump |
| `internal/config` | `config.toml` loading and host selection |
| `internal/remote` | SSH: target resolution, connect, host-key policy, auth, one-shot and streaming exec with cancellation |
| `internal/compose` | `docker compose` command builders (with shell quoting), `ps` output parsing into typed models, and both `docker stats` forms — one-shot sample and live stream |
| `internal/host` | machine resource metrics for the status view's system panel: one command over `/proc` and `df -Pk`, parsed into a typed sample |
| `internal/logs` | log engine: line assembly, tail buffer, JSONL records, field filters, stats, search |
| `internal/tui` | Bubble Tea application: app model, status and log views, the system panel and resource modes, keymaps |

| Concern | Library | Rationale |
| ------- | ------- | --------- |
| TUI | Bubble Tea + Lipgloss | De-facto standard Go TUI stack; Elm-style models are directly unit-testable |
| SSH | `golang.org/x/crypto/ssh` (+ `knownhosts`, `agent`) | Battle-tested client, the deciding factor of the Go port |
| `~/.ssh/config` | `kevinburke/ssh_config` | Alias resolution without hand-rolling a parser |
| Concurrency | goroutines + channels, `context.Context` | Cancellation and streaming map naturally onto the product |
| JSON / JSONL | stdlib `encoding/json` with `json.Number` | Keeps numeric literals verbatim in records |
| Config | `pelletier/go-toml/v2` | TOML host definitions and scripts |
| CLI args | stdlib `flag` | The surface is tiny: `linqode [host] --exec --config` |

## Flows

### Startup and host selection

The CLI argument is either a name from the config file (which contributes
`compose_dir` and scripts) or an inline `[user@]host[:port]` spec; with a
single configured host, no argument is needed. The spec is then resolved
against `~/.ssh/config` (aliases, user, port, identity files), so hosts
reachable by plain `ssh` need no extra setup. With `--exec` the command's
raw output streams to stdout/stderr and the exit code is propagated — no
TUI.

### Connecting

One SSH session is established per run; every later operation opens its own
channel over it.

- **Host key**: checked against `~/.ssh/known_hosts`, keyed by the resolved
  host as OpenSSH does — servers already trusted via plain `ssh` are
  recognized. Unknown host → show the fingerprint, ask for confirmation,
  persist on accept (trust-on-first-use). Key mismatch → refuse with the
  conflicting line number, never bypassable.
- **Auth**: SSH agent first, then the target's identity files, loaded
  lazily so no passphrase is asked for if the agent suffices; encrypted
  keys prompt with OpenSSH-style retries. Password auth is out of scope for
  the MVP.
- Prompts run in the terminal before the TUI takes over the screen.

### Compose status

The status view runs `docker compose ps --all --format json` in the
project's directory — a one-shot exec per refresh, triggered manually or by
a 5-second timer, always in a background command. Output is parsed into
typed service rows (both the NDJSON and the legacy array shape are
accepted) and rendered as a table with state/health coloring. Selection is
preserved on the same container across refreshes; a failed refresh shows
the error while the last good table stays on screen.

The same tick samples the host's load, memory, disk and uptime, as a second
exec: independent so one failing cannot blank the other, and cheap enough
(~2 ms) that the separation costs nothing. A failed sample keeps the last
one on screen, marked stale, mirroring how the table survives a failed
refresh. Those readings render as a panel down the right-hand side, with
per-resource bars and a count of services by state; below a terminal width
of 100 the panel would cost the table more than it is worth, and the same
sample collapses into a single line under the header instead.

Per-container CPU and memory are a third exec on a third interval, because
they cost two orders of magnitude more: `docker stats` needs ~2 seconds to
answer whatever the project's size, since the daemon reads each container's
cgroups twice, a second apart, to derive a CPU percentage (measured
alongside the other commands in `tests/e2e/cost_test.go`). Two modes come
out of that:

- **Soft**, the default: one `docker stats --no-stream` every 20 seconds,
  filling the table's CPU and MEM columns. A whole sample replaces the
  previous one, so a container that stopped between two samples loses its
  numbers rather than freezing them.
- **Live**, on `a`: the **streaming** form over the log-follow pipeline,
  emitting a block per second into a panel below the table — current
  readings plus a CPU sparkline per container, scaled to its own peak so
  fluctuation is visible at any magnitude. While it runs the soft poll
  stands down; closing it terminates the remote command and the poll takes
  the columns back.

Both are scoped to the project's container ids, since a bare `docker stats`
would report every container on the host. Docker wraps its output in
cursor-control escapes even when writing to a pipe, so the parser strips
them before reading the JSON.

### Following logs

Opening a service starts `docker compose logs --follow` (no prefix, no
color, tailing recent history) on a streaming exec channel. From there:

1. the feed pump (a goroutine in `cmd/linqode`) receives stdout/stderr
   byte chunks, reassembles complete lines across arbitrary chunk
   boundaries, and sends line events into a buffered channel (stderr is
   tracked separately as diagnostics);
2. the log view drains that channel every 100ms — with an upper bound per
   tick, so a log burst cannot starve input handling — into the engine's
   store: a bounded tail buffer (oldest lines drop when full) that parses
   each line on entry;
3. the view renders the visible slice, following the tail until the user
   scrolls up; jumping to the bottom re-enters follow mode.

Search (`/`, then `n`/`N`) runs over the visible lines with wrap-around
and match highlighting. Closing the view cancels the remote command
(terminate signal, then channel close) and tears down the pipeline.

### Structured logs

Every incoming line is offered to the JSONL parser; a line that is a JSON
object becomes a record whose nested fields are flattened to dotted paths
(`http.status=500`). On top of these records:

- **Detection**: when most buffered lines parse as records, the view
  switches to structured rendering — timestamp, colored level, message,
  then remaining fields — with a manual override either way (`s`).
  Well-known level/message/timestamp key variants are recognized.
- **Filters** (`f`): `key=value` / `key!=value` terms (AND-ed,
  case-insensitive) narrow the visible view to matching records;
  plain-text lines are hidden while a filter is active. Scroll, search,
  and follow all operate on the filtered view, whose indices are kept
  consistent across buffer drops by sequence-number accounting.
- **Stats** (`a`): counts by level and top values of a chosen field (`t`),
  shown in a side panel. They are recomputed on demand over the bounded
  tail (see [porting.md](porting.md), deliberate divergences).

### Service actions and scripts

Restart/stop/start on the selected service build the corresponding
`docker compose` command; predefined scripts from the config run verbatim
on the host (no compose-dir `cd`). Both reuse the log-follow pipeline: the
command's output streams into the same view, the exit code is shown on
completion, and the status view refreshes on return — so a restart's
effect is visible immediately.

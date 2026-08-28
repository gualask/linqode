# Architecture

_Last updated: 2026-08-08_

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
┌──────────────────────────── laptop ────────────────────────────┐
│  TUI adapter (Bubble Tea)        JSON/JSONL CLI adapter         │
│             └──────────────┬──────────────┘                     │
│                    typed operations                             │
│          status · stats · logs · actions · scripts              │
│                  ┌─────────┴─────────┐                          │
│       Compose/host/log parsers    SSH session                   │
│       and command builders        streamed channels             │
└────────────────────────────────┬────────────────────────────────┘
                                 │ SSH
                    ┌────────────▼────────────┐
                    │ sshd → docker compose  │
                    │        or named script │
                    └─────────────────────────┘
```

Four principles shape the design:

- **The UI loop never blocks.** Every SSH round-trip runs inside a Bubble
  Tea command (a background goroutine) whose outcome comes back as a
  message; streamed output flows through channels drained on a bounded
  tick. Rendering and input handling never wait on the network.
- **The log engine is Docker-agnostic**: it consumes generic streams of
  bytes and lines, so it can later be pointed at plain files (`tail -F`
  over SSH) or any other remote command without changes.
- **Remote workflows have one owner.** The TUI and machine adapter both call
  `internal/operations`; neither reconstructs commands or owns service/script
  validation.
- **The machine boundary is capability-based.** It receives typed observation,
  lifecycle, and configured-script methods, never arbitrary execution. The
  TUI alone receives the human `!` capability.

## Package layout

| Package | Responsibility |
| ------- | -------------- |
| `cmd/linqode` | composition root: route selection, configuration loading, SSH connection, TUI/CLI wiring |
| `internal/cli` | machine command parsing, JSON/JSONL presentation, typed errors and process exit mapping |
| `internal/operations` | shared configured catalog and connected status/stats/log/action/script workflows; presentation-neutral streams |
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
| CLI args | stdlib `flag` | Small explicit router and one flag set per command |

## Flows

### Startup and host selection

The human route is `linqode [host]` or `linqode tui [host]`. It accepts a
configured name or inline `[user@]host[:port]`, permits a TUI-only `--config`,
and can infer the host when the config contains exactly one. The selected spec
is resolved against `~/.ssh/config`, including aliases, user, port, and
identity files.

Top-level machine command names are reserved. Those routes always load the
default TOML and accept exact configured host names only; unknown values never
fall back to inline SSH targets. `hosts` and `scripts` use the local catalog
without resolving or connecting to SSH.

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
- Prompts run in the terminal before the TUI takes over the screen. The machine
  connector instead fails closed: it never learns an unknown key or requests a
  passphrase, and maps authentication failures to typed JSON.

### Machine commands

The command reference and output/exit contract live in the
[README](../README.md#machine-interface). After strict local selection, the
composition root creates one SSH session and hands `internal/cli` a narrow
safe interface implemented by the same `HostOperator` used by the TUI.

One-shot status and stats become versioned JSON documents. Followed stats,
logs, lifecycle actions, and scripts project the shared operation feed into
JSON Lines. Remote stderr is an event in that stdout stream; Linqode's own
typed errors use stderr. Read failures use Linqode exit codes, while a started
lifecycle action or script propagates a reported non-zero remote status. No
command is retried or reconnected automatically.

### Compose status

The status view runs `docker compose ps --all --format json` in the
project's directory — a one-shot exec per refresh, triggered manually or by
a 5-second timer, always in a background command. Output is parsed into
typed service rows (both the NDJSON and the legacy array shape are
accepted) and rendered as a table with state/health coloring. Selection is
preserved on the same container across refreshes; a failed refresh shows
the error while the last good table stays on screen.

Restart counts are not in that output, so the same refresh follows it with
`docker inspect --format '{{.Name}} {{.RestartCount}}'` over the containers
`ps` just named — cheaper than a second compose invocation, which would pay
the compose CLI's startup again to re-derive a list already in hand. The
reading is best-effort: it never fails a refresh, and inspect's non-zero
exit is ignored, since a container that disappeared between the two
commands makes it fail while the remaining lines are still good. Counts
that did not arrive render as `-`, distinct from a container that has
genuinely never restarted; the RESTARTS column appears only once something
can fill it.

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

1. the shared operation stream receives stdout/stderr byte chunks, reassembles
   complete lines across arbitrary chunk boundaries, and emits typed events
   through a buffered channel;
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

### Service actions, scripts, and ad-hoc commands

Restart/stop/start on the selected service build the corresponding
`docker compose` command; predefined scripts from the config, and commands
typed at the `!` prompt, run verbatim on the host. That split is the rule:
what Linqode builds is project-relative and gets a compose-dir `cd`, what
the user supplies runs where `ssh host 'command'` would run it — the same
login directory as a normal remote shell command.

All three reuse the shared operation feed. The TUI maps it into the log view,
shows the exit code, and refreshes status on return; the machine adapter maps
it into JSONL. Ad-hoc commands remain available only through the human `!`
path. Machine scripts are resolved by configured name and receive no runtime
arguments.

Two input shapes carry them, and both are modal — while one is up, keys go
to it instead of to the table:

- **Menus** (`c` actions, `x` scripts) share one `menu` type: a list of
  entries, each a label plus the command it runs, navigated with `j`/`k`
  and dismissed with `esc` or the key that opened it. Service actions live
  here rather than on a key each so that **no two keys in the application
  differ only by the shift key** — `s` and `S` for stop and start put a
  service one mistyped capital away from the opposite outcome. Showing the
  command before running it falls out of the same design.
- **The `!` prompt** is an inline footer input like the log view's `/` and
  `f`: every key edits the line, and it reopens holding the last command so
  a typo is corrected rather than retyped.

The case rule applies to what Linqode invents. The `j`/`k`, `g`/`G` and
`n`/`N` pairs stay as they are: they are the vim and less bindings every
terminal user already has in their fingers, and getting one wrong moves the
cursor rather than a service.

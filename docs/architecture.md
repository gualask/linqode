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
| `internal/host` | machine resource metrics for the header band and the system view: one marker-sectioned command over `/proc` and `df -Pk`, parsed into a typed sample, plus the client-side deltas that turn its counters into percentages and rates |
| `internal/logs` | log engine: line assembly, tail buffer, JSONL records, field filters, stats, search |
| `internal/tui` | Bubble Tea application: the app model routing between the home screen and the follow view, and the backend adapting operations to background commands |
| `internal/tui/home` | the home screen: header, layout, focus, footer, the modal menus, the `!` prompt, and the sampler that decides what is read off the host and how often. It owns the screen; a feature package owns only what is inside its own panel |
| `internal/tui/status` | the services panel: the compose table, its columns, and the container readings behind them |
| `internal/tui/system` | the machine: the header band, and the system view an `enter` on it opens |
| `internal/tui/events` | the feed of what the daemon reported happening, fed by the stream the refresh already runs |
| `internal/tui/follow` | the full-screen view for a log, action, script, or ad-hoc command |
| `internal/tui/panel` | the chrome a focusable region wears: a titled box that occupies exactly the cells it was given, the focus tokens, and the footer hints |
| `internal/tui/theme` | the visual tokens every view shares: named adaptive colors rather than the terminal's ANSI slots, so what an operator sees does not depend on their color scheme |

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

**One owner of the cadence.** Everything the screen reads off the host goes
through a sampler in `internal/tui/home`: one heartbeat a second asks each
source whether it is due. A source carries its own interval, refuses to
overlap itself, and is skipped entirely when nobody is looking at what it
feeds — the container readings stand down while the live stream is filling
the same columns. A read that takes longer than a quarter of its own interval
stretches it: on a link where `ps` takes a second, asking every five would
keep a command in flight most of the time, and the honest response is to ask
less often rather than to queue reads that overlap. Panels render what they
are handed; none of them owns a timer. `r` reads everything again on demand,
whatever the intervals say.

The services source runs `docker compose ps --all --format json` in the
project's directory — a one-shot exec per read, always in a background
command. **What triggers that read is the daemon, not a clock**: one
`docker events` stream, scoped by the project label `ps` reported and
filtered server-side to the actions that change a row, re-reads the table as
soon as something happens. The interval stays as a sixty-second safety net
(see PROJECT.md, "The daemon is watched, not polled"). News arriving while a
read is already in flight is remembered rather than dropped: that read
answers a question older than the news, so another follows it.

Those same events are also worth reading on their own, which is the **events
panel**: a satellite under the table showing what the daemon reported, newest
first, each line read for what it means rather than printed as the daemon
wrote it — an exit code of 137 is a container that was killed, and that is
not visible anywhere else on the screen once the row is gone. It answers the
question a table structurally cannot: a table is a statement about now, and
what an operator usually needs to know is *when* something happened and in
what order. It costs nothing extra on the wire — the stream is already
running for the refresh — which is the only reason it earns a place at all.
Each event carries the daemon's own timestamp rather than the moment the line
was read: normally the two differ by the drain interval, but a link that
stalls and then delivers a burst is exactly when the feed is worth reading,
and client-side stamping would give every event in that burst the same wrong
time. `enter` on one opens the logs of the container it happened to, mapping
the container back to its service through the list the table already holds.

The panel is the first **satellite**, and it establishes how satellites
behave: it is in the focus ring but not always on the screen, it has a fixed
height, and it is what gives its rows back when the terminal is too short to
hold both — the anchor never shrinks below what makes it a table. Focus skips
it while it is not drawn, and leaves it if the terminal shrinks under it. It
is also laid out whenever the session has a stream at all, empty or not: an
empty feed says the daemon is being watched, which is worth knowing, and a
panel that appeared the first time a container died would move the table
under the operator at the worst possible moment. Output is parsed into
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

The screen samples the machine on its own interval, as a second exec:
independent so one failing cannot blank the other, and cheap enough (~6 ms)
that the separation costs nothing. One command carries all of it — load,
uptime, memory and swap, per-core CPU, every network interface, pressure,
the root filesystem and the full mount list — because the batch's markers
mean a reading added to it costs bytes and not a round trip. Two of those
readings are counters rather than values: `/proc/stat` and `/proc/net/dev`
count since boot, and the percentage and the rate are the difference between
two samples, computed here rather than by asking the server to sleep between
two of its own. The interval that difference is divided by is the host's own
uptime, so a slow link cannot turn a quiet second into a spike. The
sampling belongs to the screen rather than to either panel, because the same
sample feeds the band and the system view behind it. A failed sample keeps
the last one on screen, marked stale, mirroring how the table survives a
failed refresh. Those readings render as a band of htop-style meters under the
header: one per resource, the bar carrying the percentage and the text
inside it the absolute amounts, so the percentage is never printed twice.
The bars share whatever the labels leave, stretching with the terminal, and
shed the least urgent parts — uptime first, then meters from the bottom up
— rather than overflowing, and are capped at thirty cells because past
that a gauge adds resolution nobody reads. The band is labelled `host`: CPU
and memory appear twice on this screen, and without the word it reads as an
aggregate of the rows below. The count of services by state rides on the
title line beside the target.

Which readings the band carries follows from what they cost the row. The
headline is the real CPU percentage, and the load average until there is one
— a percentage is a difference between two samples, so it does not exist
until the second. There is one disk meter and it follows the **fullest**
filesystem, labelled with its mount point: a comfortable `/` says nothing
about the `/var/lib/docker` that is about to stop the deployment, which is
among the most common ways one does. Swap appears only once a meaningful
share of it is in use, because almost every healthy Linux machine has a
little swapped out and a machine that is *filling* swap is in trouble
`MemAvailable` does not show.

The band is also the first stop in the focus ring, without a border it has no
room for: its label carries the focus instead. `enter` on it opens the
**system view**, the same sample with the room to print what one row has to
leave out: the CPU average over a strip of one cell per core (which is what
makes the average readable — one pinned core among eight idle ones is a
machine with a problem and an average that says twelve percent), the other
two load figures, memory and swap, one row per filesystem with its device,
network throughput and the interface carrying most of it, and PSI pressure.
Rows are ordered by how much they answer "what is wrong with this machine",
because a short terminal truncates the box from the bottom.

Beside the CPU, memory and network rows runs a column of **trend strips**,
drawn from samples already fetched — the one reading on the screen that
costs the server nothing. Each is scaled against its own window rather than
against 0–100, because the meter beside it already says the level: memory
sitting between 78% and 88% on a fixed scale draws as eight solid blocks and
the climb inside it disappears, which is the whole thing the strip is there
for. The window is widened to a floor so a reading that never moves is not
drawn as if it had swung end to end, and colour still comes from the raw
value, so height means "how it moved" and colour means "how bad". Throughput
has no natural full and is scaled against the busiest moment in its window
instead. The strips appear as a column or not at all: one showing up on a
single row would read as data about that row rather than as the terminal
running out of width, so they are what a narrow screen gives up. Nothing is
retained across sessions — history while nobody is connected is what an
agent would buy, and it is a non-goal. That view is
where every later measurement belongs — temperatures, GPU, the processes
behind these numbers — rather than in another box on the home, which is what
keeps the home cheap to draw and cheap to sample. `esc` comes back.

The table is a panel: it sits in a titled box whose border says which region
the keys are talking to, and it draws its heading as a band across the box's
full width, the way htop and k9s do, so a table narrower than the terminal
reads as occupying its space rather than trailing off part way; a blank line
separates the box from the header block. Focus is carried by the border and
title color, never by reverse video, which already marks the selected row —
and a panel that does not hold focus draws that row as a quiet fill instead of
a lit bar. The gap between columns is the widest of four, three or
two spaces whose layout still fits, so a wide terminal spends its slack on
breathing room and a narrow one spends it on content.

Per-container CPU, memory and I/O are a third source, read from the kernel
rather than asked of the daemon: the cgroup files under `/sys/fs/cgroup`, plus
`/proc/<pid>/net/dev` for the network counters, whose pids arrive with the
restart counts. All of it is world-readable, so the operator account needs no
privilege it did not already have. Percentages are the difference between two
readings, which is the work `docker stats` spends two seconds doing on the
server — 6 ms against 2 s, measured. Two modes follow from that: `docker stats` needs ~2 seconds to
answer whatever the project's size, since the daemon reads each container's
cgroups twice, a second apart, to derive a CPU percentage (measured
alongside the other commands in `tests/e2e/cost_test.go`). Two modes come
out of that:

- **Soft**, the default: one `docker stats --no-stream` every 20 seconds,
  filling the table's CPU, MEM, NET RX/TX and IO R/W columns. A whole sample
  replaces the previous one, so a container that stopped between two
  samples loses its numbers rather than freezing them.
- **Live**, on `a`: the **streaming** form over the log-follow pipeline,
  emitting a block per second into a panel below the table — current
  readings plus a CPU sparkline per container, scaled to its own peak so
  fluctuation is visible at any magnitude. While it runs the sampled source
  stands down — that is what its gate is for; closing it terminates the
  remote command and the sampling takes the columns back.

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

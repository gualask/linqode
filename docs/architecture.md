# Architecture

_Last updated: 2026-09-09._

The shape of the codebase: what the pieces are, which way they depend, and
how a session starts. The behaviour they implement is documented by
capability — [monitoring.md](monitoring.md), [interface.md](interface.md),
[operations.md](operations.md) — and the vision, scope and settled policy
decisions live in [PROJECT.md](PROJECT.md). Testing is [tests.md](tests.md);
how the Go codebase came to be, and the Rust reference it was ported from
(tag `rust-mvp`), is [porting.md](porting.md).

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
| `internal/local` | the same one-shot and streaming exec on the machine Linqode itself runs on, cancelled by signalling the command's own process group so that nothing it started outlives it |
| `internal/compose` | `docker compose` command builders (with shell quoting) and the parsers for what they return: `ps` output, the daemon's event stream, container cgroup counters, `docker system df`, and both `docker stats` forms |
| `internal/probe` | the connect-time capability probe: what the host can be asked for, established once in one round trip and degrading to unknown rather than to a finding |
| `internal/host` | everything about the machine itself, read out of `/proc` and `/sys`: the marker-sectioned host batch, the client-side deltas that turn its counters into percentages and rates, and the on-demand process table and graphics cards |
| `internal/logs` | log engine: line assembly, tail buffer, JSONL records, field filters, stats, search |
| `internal/tui` | Bubble Tea application: the app model routing between the home screen and the follow view, and the backend adapting operations to background commands |
| `internal/tui/home` | the home screen: header, layout, focus, footer, the modal menus, the `!` prompt, and the sampler that decides what is read off the host and how often. It owns the screen; a feature package owns only what is inside its own panel |
| `internal/tui/status` | the services panel: the compose table, its columns, and the container readings behind them |
| `internal/tui/system` | the machine: the header band, and the system view an `enter` on it opens — meters, trend strips, processes, temperatures, cards |
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

**Unless the target is the machine itself.** A `host` of `local` — the
keyword, or a configured entry whose `host` is that word — is a target value
like an alias or an inline spec, and the composition root answers it with an
`internal/local` executor: nothing to resolve, nothing to authenticate, and
no "Connecting to …" line, because that line exists for a wait that can fail
and starting a process is neither. Everything above the executor is unchanged,
including the probe. The machine interface refuses such a host
(`local_host`) and `hosts` does not list it: see
[operations.md](operations.md). The rest of the plan for local targets, and
what is not built yet, is in `LOCAL.md`.

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
- **The capability probe**: one round trip, once, before anything else runs.
  It establishes whether docker is installed, whether this user may reach the
  daemon, which compose the host has, and whether the configured `compose_dir`
  exists — four conditions that hold for the life of the session and that a
  command hitting one of them can only report opaquely. The same batch also
  carries the host's own name, `PRETTY_NAME` from `/etc/os-release`, which
  nothing turns on and which the system view prints as its last row. Measured
  at 40 ms and 148 bytes; see [monitoring.md](monitoring.md).

  Nothing it finds refuses the session. What it establishes becomes a nil
  fetch in the backend, which is how this codebase already says a host does
  not offer something, plus the sentence saying why — so a host with no docker
  keeps its meters, its filesystems, its process table and its scripts, and
  the screen is built around what is left. The machine adapter turns the same
  findings into typed failure kinds (`docker_permission_denied`,
  `docker_unavailable`, `compose_dir_missing`, `compose_unavailable`) before a
  command runs. What it does *not* establish turns nothing off: Unknown reads
  as "carry on".

### And then

Everything after the session is open is documented by capability:

| Document | What it covers |
| -------- | -------------- |
| [monitoring.md](monitoring.md) | what is read off the host, on which tier, and what each reading cost when it was measured |
| [interface.md](interface.md) | the screen: panels, focus, the band, the system view, the feed, and what gives way when the terminal runs short |
| [operations.md](operations.md) | compose lifecycle, configured scripts, ad-hoc commands, log following, and the machine interface |

The `_Last updated_` date to trust for a behaviour is the one on its own
document. This one changes only when the package layout or the connection
flow does.

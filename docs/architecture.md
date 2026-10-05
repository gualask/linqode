# Architecture

_Last updated: 2026-10-05._

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
| `internal/local` | the same one-shot and streaming exec on the machine Linqode itself runs on, cancelled by signalling the command's own process group so that nothing it started outlives it — or the session, whose `Close` ends every command it started |
| `internal/compose` | `docker compose` command builders (with shell quoting) and the parsers for what they return: `ps` output, the daemon's event stream, container cgroup counters, `docker system df`, and both `docker stats` forms |
| `internal/probe` | the connect-time capability probe: what the host can be asked for, established once in one round trip and degrading to unknown rather than to a finding |
| `internal/host` | everything about the machine itself, read out of `/proc` and `/sys`: the marker-sectioned host batch, the client-side deltas that turn its counters into percentages and rates, and the on-demand process table and graphics cards. Plus the one exception to all of that: a native reader for a local macOS target, where those files do not exist |
| `internal/logs` | log engine: line assembly, tail buffer, JSONL records, field filters, stats, search |
| `internal/tui` | Bubble Tea application: the app model routing between the home screen and the follow view, and the backend adapting operations to background commands |
| `internal/tui/home` | the home screen: header, layout, focus, footer, the modal menus, the `!` prompt, and the sampler that decides what is read off the host and how often. It owns the screen; a feature package owns only what is inside its own panel |
| `internal/tui/status` | the services panel: the compose table, its columns, and the container readings behind them |
| `internal/tui/system` | the machine: the header band, and the system view an `enter` on it opens — meters, trend strips, processes, temperatures, cards |
| `internal/tui/events` | the feed of what the daemon reported happening, fed by the stream the refresh already runs |
| `internal/tui/follow` | the full-screen view for a log, action, script, or ad-hoc command |
| `internal/tui/spark` | the shapes readings are drawn as — a strip of one cell per sample, a bar filled by eighths, several shares drawn as one divided bar, a histogram a few rows tall, fine or solid — and the windows they are scaled against. It knows no colours: the reading's style and the track come from the caller |
| `internal/tui/panel` | the chrome a focusable region wears: a titled box that occupies exactly the cells it was given, the focus tokens, the footer hints, and the capability a region declares when its cursor sits on a service |
| `internal/tui/theme` | the visual tokens every view shares: named adaptive colors rather than the terminal's ANSI slots, so what an operator sees does not depend on their color scheme |

### What a panel declares, rather than what the screen knows

`panel.Panel` is what every focusable region implements; `panel.ServiceRegion`
is what only some do, and it is how a key that needs a service finds one.

The keys that act on a service — `enter` for its logs, `c` for what can be
done to it — raise two questions, and both belong to the region rather than to
the screen. *Does this region deal in services at all?* is answered by the
type: the compose table and the events feed implement `ServiceRegion`, the
machine's band and its readings do not, so on them the keys are neither
advertised nor answered whatever they are showing. *Is one under the cursor,
and is the key worth offering when there is not?* is answered by the panel,
because only it knows what its own emptiness means. Both currently answer that
an empty region offers nothing — a compose project with no containers has
nothing these keys can do, and neither has a feed in which nothing has
happened — but they answer it separately, and a panel that wanted to offer a
key while its cursor sat between things would say so without the screen
learning a new special case.

The screen's part is choosing which region the keys are talking to — the panel
with focus, or the detail that has taken the body from it — and asking it.
This replaced a switch on panel identity repeated wherever a key needed a
service, which is the shape that makes a new panel an edit in three places and
made `c` open a menu about the table's selection from regions that had one of
their own, or none at all.

| Concern | Library | Rationale |
| ------- | ------- | --------- |
| TUI | Bubble Tea + Lipgloss | De-facto standard Go TUI stack; Elm-style models are directly unit-testable |
| SSH | `golang.org/x/crypto/ssh` (+ `knownhosts`, `agent`) | Battle-tested client, the deciding factor of the Go port |
| `~/.ssh/config` | `kevinburke/ssh_config` | Alias resolution without hand-rolling a parser |
| Concurrency | goroutines + channels, `context.Context` | Cancellation and streaming map naturally onto the product |
| JSON / JSONL | stdlib `encoding/json` with `json.Number` | Keeps numeric literals verbatim in records |
| Config | `pelletier/go-toml/v2` | TOML host definitions and scripts |
| Native machine readings (Darwin only) | `shirou/gopsutil/v4` | macOS has no `/proc` and no CLI exposing cumulative CPU counters; behind a build tag, reached only when the target is this machine |
| CLI args | stdlib `flag` | Small explicit router and one flag set per command |

## Flows

### Startup and host selection

The human route is `linqode [host]` or `linqode tui [host]`. It accepts a
configured name or inline `[user@]host[:port]` (an IPv6 address in brackets
when it has a port: `[::1]:2222`), permits a TUI-only `--config`,
and can infer the host when the config contains exactly one. The selected spec
is resolved against `~/.ssh/config`, including aliases, user, port,
identity files and `IdentitiesOnly` (the list is in the
[README](../README.md#getting-started)).
`ProxyJump` and `ProxyCommand` are not supported: a host that sets either is
refused at resolution, because connecting directly instead would take
another path than `ssh` does, or reach nothing.

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
  conflicting line number, never bypassable. As OpenSSH does, the host key
  algorithms offered are narrowed to the types `known_hosts` records for the
  host (an RSA key allowing its SHA-2 signatures; certificates only when a
  `@cert-authority` line applies), so a server holding ed25519 and ECDSA
  keys shows the pinned one. A server that cannot show any recorded type is
  refused as well, rather than learned: a new key type in place of the pinned
  one is verified separately, like a changed key.
- **Auth**: SSH agent first — with `IdentitiesOnly`, only its keys that
  match a configured identity file (by the `.pub` beside it, or the public
  half an OpenSSH key file carries unencrypted) — then the target's identity
  files, loaded lazily so no passphrase is asked for if the agent suffices; encrypted
  keys prompt with OpenSSH-style retries. Password auth is out of scope for
  the MVP.
- Prompts run in the terminal before the TUI takes over the screen. The machine
  connector instead fails closed: it never learns an unknown key or requests a
  passphrase, and maps authentication failures to typed JSON. An encrypted
  identity is skipped rather than ending authentication, and named as the
  failure only when no later identity is accepted.
- **The capability probe**: one round trip, once, before anything else runs.
  It establishes whether docker is installed, whether this user may reach the
  daemon, which compose the host has, whether the configured `compose_dir`
  exists, whether the host has a readable `/proc`, and which daemon
  `DOCKER_HOST` or the current docker context points at — conditions that hold for the
  life of the session and that a command hitting one of them can only report
  opaquely. The same batch also
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
  command runs. A host with no `/proc` loses the process table, which is that
  filesystem and nothing else; the docker endpoint turns nothing off and is
  shown in the header. What it does *not* establish turns nothing off: Unknown reads
  as "carry on".

### Teardown

Nothing a run started outlives it. `SIGINT`, `SIGTERM` and `SIGHUP` cancel
the run's context instead of killing the process, and `main` exits only after
everything below it has returned, so the deferred cleanup always runs. When
the screen returns — the operator quit, or a signal ended it — the context
every feed was opened on is cancelled first and the session closed second.
The screen runs on that same context, so a signal Bubble Tea does not answer
itself — `SIGHUP`, a terminal that went away — still ends it, and `tui.Run`
stops every stream it handed out before returning.

The local session is the one where this is load-bearing. Closing an SSH
connection ends every channel on it at the far end; a local command is a
process group of its own, which neither the terminal's `SIGINT` nor its
`SIGHUP` reaches. So every command a local session starts runs under the
session's context as well as its caller's, and `Close` cancels them all and
waits, bounded, until each has been reaped. A descendant that left the group
on purpose (`setsid daemon &`) is out of reach; its stream still closes,
because once the group is dead the read ends of its pipes are closed too.

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

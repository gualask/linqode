# TODO

Concrete items waiting to be picked up. The scope, the settled policies and
the roadmap live in [docs/PROJECT.md](docs/PROJECT.md) — this file is the
working list that hangs off its "Next" section, and the record of what was
looked at and not taken, so nothing is re-derived from scratch.

## Is a machine command's environment trusted?

Open question, and the only item here that touches security. Machine commands
select the default config and the SSH files through the process environment,
so `HOME` and `SSH_AUTH_SOCK` decide which config and which credentials are
used.

Either that is a capability-boundary bypass — in which case machine mode
should bind to an operator-selected immutable home, with a regression test —
or it is an accepted deployment precondition, in which case the documented
boundary in [operations.md](docs/operations.md) and
[PROJECT.md](docs/PROJECT.md) should say so in as many words. It is currently
neither.

To decide it: trace the direct machine path, demonstrate the behaviour with a
temporary home, compare against the TUI path, and reconcile with what the
README and PROJECT.md already claim.

## PTY — analysis not finished

Named in PROJECT.md as "PTY support for interactive commands (`sudo`,
prompts)". The case for it has not been made convincingly yet, and it should
not be scheduled before it is.

What is established. Commands already run from the TUI (`!`, scripts, service
actions) — all through `ExecStream`, all as `ssh host 'command'` with no
terminal allocated. What that costs: `sudo` without NOPASSWD cannot ask for a
password and fails outright; a `[y/N]` confirmation cannot be answered;
programs that check `isatty()` drop colour and progress output. A PTY (the
`ssh -t` equivalent) makes the remote program believe it is talking to a
terminal, and lets keystrokes reach it.

What that drags in. With a PTY the remote side stops emitting lines and starts
emitting screen updates — cursor moves, line clears, redraws in place. The log
view appends lines and would render garbage. `stripANSI` in
`internal/compose/stats.go` is not the answer: it discards escapes because
there they are noise around a JSON payload, whereas under a PTY the escapes
*are* the content. Rendering it means a client-side terminal emulator holding
a grid of cells (`charmbracelet/x/vt`, by the Bubble Tea authors) and a
`View()` that draws the grid — omnyssh's architecture, at 500 lines of core
plus 900 of UI, and even that refuses ProxyJump and caps scrollback at 1000
lines.

Open questions to settle before any of this is scheduled:

- How often does the blocking case actually occur? A deployment where the
  operator's account needs an interactive `sudo` for docker is one shape; most
  Compose hosts put the user in the `docker` group and never prompt. The
  capability probe above would answer this from real hosts — which argues for
  doing that first and deciding afterwards.
- Is there a cheaper answer for the narrow case? Passing a password to a
  single `sudo -S` on stdin, or simply reporting "this host needs an
  interactive sudo" and refusing, may cover the whole of it without a terminal
  emulator.
- If a PTY is warranted, does it stop at a single one-shot command (a `!`
  invocation that can prompt) or grow into interactive shell sessions? Only
  the first is in scope; the second is omnyssh's product, not this one.
- What does an emulated grid do to the log view's own affordances —
  scrollback, `/` search, the structured-mode panels? They assume a line
  buffer, and a cell grid has none of them.

## Watch, but do not act on yet

- **`internal/tui/system.View()` is 114 lines**, the longest function in the
  tree, and it grew that way across phases C through G. It is a flat sequence
  of independent blocks — one per reading, three to eight lines each, no
  nesting — so it is long rather than dense. Splitting it into a method per
  reading would scatter the one thing the sequence itself carries: the rows
  are ordered by how much they answer "what is wrong with this machine",
  because the box truncates from the bottom. Revisit if a reading is added
  that is not a straight `if reported { append row }`.

## Considered and not taken

Recorded so it is not re-examined from scratch:

- **Automatic SSH reconnection.** OpenSSH itself exits when its transport
  dies, and Linqode is intended for medium-length interactive sessions rather
  than as an always-on monitor. Reconnecting would add re-authentication
  inside the TUI and ambiguous retry semantics for commands that may already
  have run. Defer it until real deployments show that connection loss and the
  cost of restarting Linqode are recurring problems. If clearer disconnect
  handling becomes necessary first, prefer exiting with an explicit error,
  matching `ssh`, over restoring the session automatically.
- **Multi-format metric parsers.** omnyssh cascades through `top` procps-ng /
  BusyBox / macOS, `free` in 4- and 7-column shapes, `vm_stat` plus `sysctl`,
  then `/proc/stat` — each fallback a further SSH round-trip, up to eight or
  ten per poll. `internal/host` reads `/proc` and POSIX `df -Pk` in one
  command, measured at 6 ms for nine readings, and the targets are Docker
  hosts, meaning Linux. No macOS or BSD support.
- **An `awk` filter for the process list.** omnyssh's `top_processes_command`
  drops the connection's own shell and `sshd` strictly by PID, which is a good
  trick and worth remembering. It was not needed: the list is ranked on the
  client from `/proc/<pid>/stat`, where an idle `sshd` ranks nowhere near the
  top by either memory or CPU. Filtering on the server would also have meant a
  program on the server, which that reading deliberately avoids — `sort -k24`
  counts space-separated fields, and field two is a process name that may
  contain spaces.
- **Their trust-on-first-use.** An unknown host key is recorded silently with
  a log warning and no question asked. Linqode shows the fingerprint and asks;
  that policy is settled in PROJECT.md. (Both fail closed on an unreadable
  `known_hosts`, which is right.)
- **The key-setup state machine.** Its rollback design suits a one-shot
  irreversible reconfiguration of `sshd_config`; nothing here does that — the
  most destructive action is `compose stop`, undone by `start`. The one
  portable line: verify with a *fresh* connection, never the one already open,
  because a live session proves nothing about the next login.
- **SFTP panels, the Tauri GUI, multi-host dashboards.** Declared non-goals.
- **Signalling processes from the system view.** Designed and declined; see
  "The mutation surface stays narrow" in [PROJECT.md](docs/PROJECT.md).

## Closed

- **Probe host capabilities once per connection** _(September 2026)_ — landed
  as `internal/probe`: one round trip at connect, 40 ms and 148 bytes measured,
  establishing docker, the socket as this user, which compose, and whether
  `compose_dir` is there. The criterion held: nothing was added to it that only
  changes one command's fallback, and the two September guards (`timeout`,
  `nvidia-smi`) stayed inline. See the probe section in
  [monitoring.md](docs/monitoring.md), the degraded screen in
  [interface.md](docs/interface.md), and the typed failure kinds in
  [operations.md](docs/operations.md).

  Two notes for whoever comes back to it. **Compose v1 is named, not driven** —
  detecting it is a section, adapting to it would put the binary's name in
  front of every compose command this codebase builds, for a tool whose authors
  stopped shipping it in June 2023. And **the three findings the fixture cannot
  produce** — no docker, compose v1, an account outside the `docker` group —
  are three different machines and this fixture is one, so they are unit-tested
  against the CLIs' documented output and stay that way until a real deployment
  is reached. That is the same position temperatures and graphics cards are in.

  It does not answer the PTY question yet. It was going to, from real hosts;
  no real host has been touched, so that still waits on hardening.

- **Top processes in the system panel** _(September 2026)_ — landed as the
  on-demand tier's first reading; see [monitoring.md](docs/monitoring.md).
- **`host_metrics = false` and the live stats panel** _(closed by 2026-09-05
  review)_ — the capability is gated and pinned by
  `TestLiveStatsUnavailableWithoutCapability`.
- **Masked `docker compose ps -q` failure** _(closed by 2026-09-05 review)_ —
  the command is `ids=$(docker compose ps -q) || exit $?`, with tests
  asserting the propagation.
- **Package-layout instructions in `CLAUDE.md`** _(closed by 2026-09-05
  review)_ — it names `internal/cli` and `internal/operations` correctly.
- **Seven file-size and cognitive-pressure candidates from the 2026-08-08
  review** — dropped rather than done. Every file they named is now between
  159 and 250 lines, `remote.ConnectWith` is thirteen, and the `status`
  package they were largely about was split apart in the September refactor.
  Any candidate would have to be re-derived against the current tree, and the
  one that shows up when you do is recorded under "Watch" above.

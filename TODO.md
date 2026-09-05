# TODO

Concrete items waiting to be picked up. The roadmap, the scope, and the
settled policies live in [docs/PROJECT.md](docs/PROJECT.md) — this file is
the working list that hangs off its "Next" section.

Most of what follows came out of reading
[omnyssh](https://github.com/timhartmann7/omnyssh) (August 2026), an
adjacent Rust tool — multi-host dashboard, PTY terminal, SFTP, snippets —
with no Compose awareness and no log analysis. Where an item names it, the
note also records what in its implementation is a counter-example, so the
mistake is not imported along with the idea.

## Probe host capabilities once per connection

Establish the ground at connect time instead of discovering it through
failed commands: compose v2 (`docker compose`) against v1
(`docker-compose`), whether the docker socket is reachable as this user or
needs `sudo`, `/etc/os-release`, whether the configured `compose_dir` is
actually there. Compose version skew and daemons behind `sudo` are named in
the roadmap as what real hosts bring.

The mechanism already exists in `internal/host/host.go`: one round-trip,
marked sections, a missing section degrading to unknown rather than failing
the sample. Reuse it. The cost is noise beside the `compose ps` already
being paid — see the cost budget in [monitoring.md](docs/monitoring.md).

omnyssh's `ssh/probe.rs` prompted the idea and is a counter-example twice
over. Its script is `cat << 'EOF' | bash`, assuming bash, while its own
metric commands stay carefully POSIX. And it detects Docker as
`has_section("DOCKER")` — that is, "`docker ps` printed something" — so a
host where Docker runs but the user is not in the `docker` group is reported
as a host without Docker. That is precisely the case this probe exists to
tell apart.

## ~~Top processes in the system panel~~ _(done, September 2026)_

Landed as the on-demand tier's first reading — see
[monitoring.md](docs/monitoring.md), "The process table". Two notes for
whoever reads the idea below and wonders what happened to it:

**The `awk` filter was not taken.** omnyssh's `top_processes_command`
(`ssh/metrics.rs`) drops the connection's own shell and `sshd` strictly by
PID, which is a genuinely good trick and worth remembering. It was not needed
here because the list is ranked on the client from `/proc/<pid>/stat`, and an
idle `sshd` ranks nowhere near the top by either memory or CPU. Filtering on
the server would also have meant a program on the server, which the process
reading deliberately avoids: `sort -k24` counts space-separated fields and
field two is a process name that may contain spaces.

**Their calibration note was right**, and it is why this reads the whole table
rather than a top-N: on a loaded server our own chain never reaches the top
anyway, so there was nothing to filter and a client-side ranking cost nothing
extra.

## PTY — analysis not finished

Named in the roadmap as "PTY support for interactive commands (`sudo`,
prompts)". The case for it has not been made convincingly yet, and it should
not be scheduled before it is.

What is established. Commands already run from the TUI (`!`, scripts,
service actions) — all through `ExecStream`, all as `ssh host 'command'`
with no terminal allocated. What that costs: `sudo` without NOPASSWD cannot
ask for a password and fails outright; a `[y/N]` confirmation cannot be
answered; programs that check `isatty()` drop colour and progress output. A
PTY (the `ssh -t` equivalent) makes the remote program believe it is talking
to a terminal, and lets keystrokes reach it.

What that drags in. With a PTY the remote side stops emitting lines and
starts emitting screen updates — cursor moves, line clears, redraws in
place. The log view appends lines and would render garbage. `stripANSI` in
`internal/compose/stats.go` is not the answer: it discards escapes because
there they are noise around a JSON payload, whereas under a PTY the escapes
*are* the content. Rendering it means a client-side terminal emulator
holding a grid of cells (`charmbracelet/x/vt`, by the Bubble Tea authors)
and a `View()` that draws the grid — omnyssh's architecture, at 500 lines of
core plus 900 of UI, and even that refuses ProxyJump and caps scrollback at
1000 lines.

Open questions to settle before any of this is scheduled:

- How often does the blocking case actually occur? A deployment where the
  operator's account needs an interactive `sudo` for docker is one shape;
  most Compose hosts put the user in the `docker` group and never prompt.
  The capability probe above would answer this from real hosts — which
  argues for doing that first and deciding afterwards.
- Is there a cheaper answer for the narrow case? Passing a password to a
  single `sudo -S` on stdin, or simply reporting "this host needs an
  interactive sudo" and refusing, may cover the whole of it without a
  terminal emulator.
- If a PTY is warranted, does it stop at a single one-shot command (a `!`
  invocation that can prompt) or grow into interactive shell sessions?
  Only the first is in scope; the second is omnyssh's product, not this one.
- What does an emulated grid do to the log view's own affordances —
  scrollback, `/` search, the structured-mode panels? They assume a line
  buffer, and a cell grid has none of them.

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
- **Multi-format metric parsers.** omnyssh cascades through `top`
  procps-ng / BusyBox / macOS, `free` in 4- and 7-column shapes, `vm_stat`
  plus `sysctl`, then `/proc/stat` — each fallback a further SSH round-trip,
  up to eight or ten per poll. `internal/host` reads `/proc` and POSIX
  `df -Pk` in one command, measured at 6 ms for nine readings, and the
  targets are Docker hosts, meaning Linux. No macOS or BSD support.
- **Their trust-on-first-use.** An unknown host key is recorded silently
  with a log warning and no question asked. Linqode shows the fingerprint
  and asks; that policy is settled in PROJECT.md. (Both fail closed on an
  unreadable `known_hosts`, which is right.)
- **The key-setup state machine.** Its rollback design suits a one-shot
  irreversible reconfiguration of `sshd_config`; nothing here does that —
  the most destructive action is `compose stop`, undone by `start`. The one
  portable line: verify with a *fresh* connection, never the one already
  open, because a live session proves nothing about the next login.
- **SFTP panels, the Tauri GUI, multi-host dashboards.** Declared non-goals.

## Pending-work review follow-up (2026-08-08)

### Next steps

- [ ] Close the caller-controlled `HOME` machine-boundary candidate — done when: the threat model is decided and either machine commands cannot select a caller-supplied config/SSH home, with a regression test, or the documented boundary explicitly treats the process environment as trusted ([`cmd/linqode/main.go`](cmd/linqode/main.go), F1).
- [ ] Close the `host_metrics = false` live-stats candidate — done when: the TUI resource behavior matches the documented opt-out and a view/backend test pins the decision ([`cmd/linqode/interactive.go`](cmd/linqode/interactive.go), F2).
- [ ] Close the masked `docker compose ps -q` failure candidate — done when: stats return the documented read failure when project discovery fails, with command and machine-adapter regression coverage ([`internal/compose/command.go`](internal/compose/command.go), F3).
- [ ] Update the authoritative package-layout instructions — done when: `CLAUDE.md` names `internal/cli` and `internal/operations` consistently with the current tree ([`CLAUDE.md`](CLAUDE.md), F4).
- [ ] Assess and resolve the `tests/e2e/e2e_test.go` file-size trigger — done when: `cf-split` confirms or rejects the candidate and any confirmed test regrouping preserves the e2e suite ([`tests/e2e/e2e_test.go`](tests/e2e/e2e_test.go), F6).
- [ ] Assess and resolve the [`internal/tui/status/stats_test.go`](internal/tui/status/stats_test.go) file-size trigger — done when: `cf-split` confirms or rejects the candidate and any confirmed regrouping preserves all stats scenarios (F8).
- [ ] Assess and resolve the [`internal/tui/status/model_test.go`](internal/tui/status/model_test.go) file-size trigger — done when: `cf-split` confirms or rejects the candidate and any confirmed regrouping preserves all status scenarios (F9).
- [ ] Assess and resolve the `internal/remote/session_test.go` file-size trigger — done when: `cf-split` confirms or rejects the candidate and any confirmed regrouping keeps the SSH integration suite green ([`internal/remote/session_test.go`](internal/remote/session_test.go), F11).
- [ ] Assess and resolve the `cmd/linqode/main_test.go` file-size trigger — done when: `cf-split` confirms or rejects the candidate and any confirmed regrouping keeps composition-root coverage green ([`cmd/linqode/main_test.go`](cmd/linqode/main_test.go), F12).
- [ ] Assess and resolve `remote.ConnectWith` cognitive pressure — done when: `cf-cognitive` confirms or rejects the candidate and any confirmed refactor preserves connection, cancellation, and typed-error behavior ([`internal/remote/session.go`](internal/remote/session.go), F13).
- [ ] Assess and resolve `TestMachineBinaryCommands` cognitive pressure — done when: `cf-cognitive` confirms or rejects the candidate and the compiled-binary scenarios remain independently diagnosable ([`tests/e2e/machine_test.go`](tests/e2e/machine_test.go), F14).

### Open questions

- Is the environment of a machine-command invocation trusted, specifically `HOME` and `SSH_AUTH_SOCK`?
  - Impact: decides whether selecting the default config and SSH files through process environment is a capability-boundary bypass or an accepted deployment precondition.
  - Possible direction: if the caller controls the environment, bind machine mode to an operator-selected immutable home/config source; this remains a hypothesis until the invocation model is confirmed.
  - Needed to decide: trace the direct machine path, demonstrate the observable behavior with a temporary home, compare the TUI path, and reconcile it with the policy in `README.md` and `docs/PROJECT.md`.

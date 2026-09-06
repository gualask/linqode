# Linqode — Project Document

_Last updated: 2026-09-06._

Vision, scope, settled decisions, and roadmap. How the system works is
documented by capability — the codebase's shape in
[architecture.md](architecture.md), what is read off a host in
[monitoring.md](monitoring.md), the screen in [interface.md](interface.md),
what the tool does to a host in [operations.md](operations.md). Testing is
[tests.md](tests.md); the Go porting plan is [porting.md](porting.md); user
setup (install, configuration, keys) is the top-level
[README](../README.md).

## Vision

A single-binary TUI that runs on the operator's machine and connects to remote
servers over SSH to **monitor and operate what runs there**: watch service
state, follow and analyze logs, and execute commands — Docker Compose
operations, plain Linux commands, or shell scripts. The tool is **agentless**:
nothing is installed on the server — Linqode drives standard CLIs remotely
over SSH and interprets their output locally.

The same binary is also a constrained SSH wrapper for automation: an agent can
use typed JSON commands and operator-configured server scripts, but cannot ask
Linqode to execute an arbitrary command. The TUI remains the human interface;
the machine surface is deliberately non-interactive.

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
5. **Agent-safe machine interface**: discover configured names, inspect the
   project, run typed lifecycle actions, and invoke exact configured scripts
   through JSON/JSONL without exposing arbitrary SSH execution.

### Non-goals (for now)

- Kubernetes, Swarm, Podman.
- Multi-host dashboards / fleet management (single host per session first).
- A server-side agent or daemon of any kind.
- Retention: nothing is kept once the session ends. History while nobody is
  connected, alerting, and fleet are what an agent would buy, and all three
  are out.
- Acting on the host beyond the compose lifecycle and the operator's own
  configured scripts — see "The mutation surface stays narrow" below.

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
- **Agent-safe machine boundary** _(decided 2026-08-08)_. Machine commands use
  only exact host and script names from the default operator-controlled TOML;
  inline targets, `--config`, arbitrary execution, and runtime script arguments
  are unavailable. Authentication never prompts or learns an unknown host key.
  Results are JSON/JSONL and mutations are never retried after an uncertain
  outcome. This is a capability guardrail, not an adversarial sandbox: the
  agent must not be able to modify the config, SSH credentials, or Linqode
  binary. The TUI keeps inline hosts, prompts, and its explicit human `!`
  command. Exact command syntax lives in the [README](../README.md#machine-interface).

  **The process environment is part of that precondition** _(decided
  2026-09-06)_. `HOME` locates the default TOML and the SSH files,
  `SSH_AUTH_SOCK` the agent, so whoever sets the environment decides which
  config a machine command obeys: `HOME=/tmp/mine linqode script prod backup`
  runs whatever *that* config calls `backup`. Binding machine mode to a fixed
  path was weighed and declined — it would pin the config and leave the
  credentials where they are, break the per-user install under `~/.config`,
  and defend only against someone already running processes as the operator,
  who can then run the command directly rather than persuade Linqode to. The
  guardrail is over an environment the operator controls, and that is now
  said rather than implied.
- **Keymap: no case-variant pairs** _(decided 2026-08-01)_. Two keys that
  differ only by the shift key must never do different things. The cost is
  not confusion but damage: `s` for stop beside `S` for start puts a
  production service one mistyped capital away from the opposite outcome.
  Where several related commands need a home, they go behind a menu (`c`
  for service actions, `x` for scripts), which also shows the exact command
  before running it. **The vim aliases were removed in September 2026** and
  the exemption went with most of them: `j`/`k`, `g`/`G` and `l` were second
  spellings of keys every terminal already sends, and a keymap with two ways
  to say "down" is one an operator learns twice. Navigation is the arrows,
  `PgUp`/`PgDn` and `Home`/`End`, the same set in every list. `n`/`N` stays —
  it is the one pair that is not an alias, since "previous match" has no
  arrow, and getting it wrong moves the cursor rather than a service.
- **A server pays rent for what is on screen, and by the second only while
  someone is watching** _(measured 2026-08-01, extended through 2026-09-05)_.
  Every reading sits on one of three tiers — event-driven, always-on, or read
  only while the view that shows it is open — and which tier it sits on is
  decided by a measurement, not by intuition. The tiers, the numbers, and the
  reasoning behind each placement are in [monitoring.md](monitoring.md).

  Two rules that follow and are not negotiable: **no reading joins the
  always-on tier without being measured**, and a measurement over the loopback
  fixture says what a command costs *on the server* and nothing about what a
  real link adds.

- **The daemon is watched, not polled** _(decided 2026-09-04)_. The service
  list is re-read when `docker events` says something changed, not on a
  five-second timer: an idle deployment costs nothing to keep on screen, and a
  container that dies appears as soon as it dies. The stream is filtered on
  the server, the timer steps back to a sixty-second safety net rather than
  away, and watching is an optimisation rather than a capability — a daemon
  that refuses the stream leaves the screen on its timer and says nothing.
  Written up in [monitoring.md](monitoring.md).

- **Agentless, and root is not needed** _(reconsidered and kept, September
  2026)_. Linqode monitors when the operator wants to monitor and helps them
  investigate the host; it is not a monitoring system. The alternatives — a
  tool installed on the server, an agent, a tunnelled daemon API — were
  weighed explicitly and are compared in [monitoring.md](monitoring.md), along
  with why almost nothing worth reading requires privilege.

- **The mutation surface stays narrow** _(decided 2026-09-05)_. Linqode drives
  the **compose lifecycle** and runs the **shell scripts the operator
  configured in the TOML**. That is the whole of what it does to a host, plus
  the `!` prompt, which is an explicitly human capability and is never given
  to the machine interface.

  Signalling processes from the system view — `kill` from the process list —
  was designed and **declined**. It would widen the surface from "this
  project's containers and the operator's own scripts" to "operations on the
  system", which is a different product. An operator who needs to kill
  something has `!`, and an operator who needs it often should put it in a
  configured script where it gets a name and a confirmation. The system view
  is an investigation surface, and investigation is a read.

## Roadmap

### Rust MVP — reference implementation _(done, July 2026; tagged `rust-mvp`)_

- **M1 — plumbing**: SSH connect + run a one-shot remote command, output in a
  minimal ratatui screen.
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

### Go port _(done, July 2026 — plan and record in [porting.md](porting.md))_

The port was feature-driven, not a 1:1 translation: the Rust tree supplied
settled answers and test fixtures, but each feature was built the way Go
builds it best (divergences recorded in porting.md). Each milestone left an
offline `go test ./...` green:

- **G0 — scaffolding** _(done, July 2026)_: Go module, package layout, CI;
  config loading + host selection.
- **G1 — connect and remote exec** _(done, July 2026)_: connect like plain
  `ssh` (host-key TOFU, agent → identity-file auth), one-shot and streaming
  exec with cancellation; integration tests against a scripted loopback
  server.
- **G2 — compose status view** _(done, July 2026)_.
- **G3 — log following** _(done, July 2026)_.
- **G4 — structured log analysis** _(done, July 2026)_.
- **G5 — actions and scripts** _(done, July 2026)_. MVP feature set
  covered: the Rust tree is removed (still available at the `rust-mvp`
  tag).

### E2E validation _(done, August 2026)_

The `tests/fixture/` sshd + docker-in-docker fixture (see
[tests.md](tests.md)) validates connect → ps → logs → stats → actions against
a live Docker daemon, behind the `e2e` build tag. It also invokes the compiled
binary through representative machine reads and mutations. This is the first
time any milestone — Rust or Go — has run against real Docker rather than
captured output.

### MVP gaps closed _(done, August 2026)_

The two items the MVP scope named but the port left open:

- **Restart counts** in the status view. `compose ps` does not report them,
  so the refresh follows it with a `docker inspect` over the containers it
  just named — one cheap daemon round-trip rather than a second compose
  invocation. Best-effort: a host where it fails keeps every other column,
  and an unknown count shows as `-` rather than as a zero.
- **Ad-hoc commands** from the TUI (`!`), streaming into the same view as
  actions and scripts. This settles where user-supplied commands run:
  scripts and `!` run in the login directory, like `ssh host 'command'`; only
  the commands Linqode builds itself are project-relative.

### Agent-safe interface _(done, August 2026)_

The JSON/JSONL interface reuses the TUI's typed operations while withholding
its ad-hoc capability. Strict local catalog selection, fail-closed SSH auth,
streamed errors, exact remote mutation exit codes, and compiled-binary Docker
coverage establish the boundary described in the decided policy above.

### The dashboard _(done, September 2026)_

The screen became a set of focusable panels with detail views behind them, and
the sampling behind it became a single owner of the cadence with three tiers.
In order: the panel model and the focus ring; the sampler, the daemon's event
stream, and container counters read off the kernel instead of asked of the
daemon; nine host readings where there were four, plus trend strips; the
events feed; the process table and `docker system df` on the on-demand tier;
temperatures; graphics cards.

The reasoning that survived is in [monitoring.md](monitoring.md) and
[interface.md](interface.md); the numbers are in the cost budget there. What
each phase cost and what it caught is in the git history — the commits are
written to be read.

### Next

- **Hardening against real deployments**: the fixture is a controlled
  Alpine/dind environment. Real hosts bring compose version skew, larger
  projects, slower links, daemons behind `sudo`, and hosts reached through
  `~/.ssh/config` rather than an inline spec. It is also the only place the
  temperature and GPU readings can be validated at all: there is no sensor and
  no card on the fixture, in the VM under it, or anywhere the suite can reach.
- **Generic remote operations** (the broadened vision): additional typed
  integrations, PTY support for interactive human commands (`sudo`, prompts),
  and monitoring plain files via `tail -F`. To be scoped into milestones.
- **Release engineering** _(deferred to the first release)_: CI currently only
  vets the e2e package (`go vet -tags e2e`), never runs it — the fixture stays
  a local step. Running it on CI is feasible whenever it is wanted:
  `ubuntu-latest` ships Docker and allows privileged containers, at the cost
  of a couple of minutes per run and some flakiness risk. Decide when cutting
  the first release, along with whatever else that needs (build matrix,
  artifacts, versioning).

### Open, and deliberately not decided

- **The Engine API over `docker system dial-stdio`** instead of the compose
  CLI. It would remove the CLI's startup from every `ps` and give native event
  and stats streams. It is less compelling than it was — the two costs that
  justified it are gone by other routes, leaving 65 ms once a minute — and it
  is a policy change, since the principle today is to drive standard CLIs
  remotely and interpret their output locally. Decide before building on it;
  it need not be all-or-nothing, as reads could move while mutations stay on
  `docker compose`. Written up in [monitoring.md](monitoring.md).

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

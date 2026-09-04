# Linqode — Project Document

_Last updated: 2026-08-08_

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
- **Agent-safe machine boundary** _(decided 2026-08-08)_. Machine commands use
  only exact host and script names from the default operator-controlled TOML;
  inline targets, `--config`, arbitrary execution, and runtime script arguments
  are unavailable. Authentication never prompts or learns an unknown host key.
  Results are JSON/JSONL and mutations are never retried after an uncertain
  outcome. This is a capability guardrail, not an adversarial sandbox: the
  agent must not be able to modify the config, SSH credentials, or Linqode
  binary. The TUI keeps inline hosts, prompts, and its explicit human `!`
  command. Exact command syntax lives in the [README](../README.md#machine-interface).
- **Keymap: no case-variant pairs** _(decided 2026-08-01)_. Two keys that
  differ only by the shift key must never do different things. The cost is
  not confusion but damage: `s` for stop beside `S` for start puts a
  production service one mistyped capital away from the opposite outcome.
  Where several related commands need a home, they go behind a menu (`c`
  for service actions, `x` for scripts), which also shows the exact command
  before running it. The exemption is the bindings Linqode did not invent —
  `j`/`k`, `g`/`G`, `n`/`N` are vim and less conventions users already
  have in their fingers, and getting one wrong moves the cursor, not a
  service.
- **Dashboard cost budget** _(measured 2026-08-01; reproduce with
  `go test -tags e2e ./tests/e2e/ -run TestRemoteCommandCost -cost.measure`)_.
  Round-trip cost of the status view's candidate commands, against the e2e
  fixture:

  | Command | 4 containers | 30 containers |
  | ------- | ------------ | ------------- |
  | exec overhead (`true`) | 1 ms | 1 ms |
  | host metrics (`/proc` + `df -Pk`) | 2 ms | 2 ms |
  | container cgroups (`/sys/fs/cgroup` + `/proc/<pid>/net/dev`) | 6 ms | — |
  | `compose ps --all --format json` | 64 ms | 60 ms |
  | `docker stats --no-stream` | 2.01 s | 2.07 s |

  _(cgroups measured 2026-09-04; the others 2026-08-01. All against the
  loopback fixture, which is why they say what a command costs on the server
  and nothing about what a real link adds — see the sampler's stretch rule.)_

  Host metrics therefore belong in the automatic refresh: their cost is
  noise beside the `ps` already being paid, so they are always on
  (`host_metrics = false` opts out for hosts where even that is unwelcome).

  `docker stats` is not on that path any more _(revised 2026-09-04)_. Its
  ~2 s is fixed sampling latency — the daemon reads each container's cgroups
  twice, a second apart, to compute CPU% — so it can never be made quick.
  **The TUI reads the same cgroups itself**, at 6 ms, because the second
  reading is the previous sample, which the client already has. Everything
  needed is world-readable: the cgroup files, and `/proc/<pid>/net/dev` for
  the network counters, whose pids come from the `docker inspect` the refresh
  already runs. Two modes follow:

  - **Sampled**, always on: the cgroup counters every **5 s**, behind the
    table's CPU, MEM, NET RX/TX and IO R/W columns. The interval is now what
    an operator can use rather than what the server can bear, and five
    seconds means the first CPU percentage — which needs two readings to
    exist at all — arrives while they are still looking.
  - **Live**, on request (`a`): `docker stats` in its streaming form, a
    sample per second, for as long as the panel is open. Streaming is the
    one form where docker's own sampling is not a tax, and it stays the
    source of the sparklines. The sampled source stands down while it runs,
    and the remote command is terminated when it closes.

  `docker stats --no-stream` remains the machine interface's one-shot: an
  agent asking once for a JSON reading is not holding a dashboard open, and
  a single answer that needs no previous reading is worth two seconds to it.

  The rule this encodes: **a server pays a small fixed rent for what is on
  screen, and pays by the second only while someone is watching**.

- **The daemon is watched, not polled** _(decided 2026-09-04)_. The service
  list is re-read when `docker events` says something changed, not on a
  five-second timer: an idle deployment costs nothing to keep on screen, and
  a container that dies appears as soon as it dies rather than within the
  next interval. Three things make this a policy rather than an optimisation:

  - The stream is **filtered on the server**, by project label and by an
    explicit list of actions. Health checks emit `exec_create`, `exec_start`
    and `exec_die` for every probe of every container — measured at thirty of
    thirty-seven events in ten seconds against a single container probing
    every two seconds — so an unfiltered stream would cost more bandwidth on
    an idle project than the polling it replaces. The project label is what
    keeps another tenant's containers on a shared host out of this session.
  - The timer **steps back rather than away**: while the stream is up the
    service list still refreshes every sixty seconds, for what no event
    describes and for a stream that stopped delivering without saying so.
    Losing the stream restores the short interval.
  - Watching is an **optimisation, not a capability**. A daemon that refuses
    the stream leaves the screen on its timer and says nothing: there is
    nothing an operator could do about it.

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

### Next

- **Hardening against real deployments**: the fixture is a controlled
  Alpine/dind environment. Real hosts bring compose version skew, larger
  projects, slower links, daemons behind `sudo`, and hosts reached through
  `~/.ssh/config` rather than an inline spec.
- **Generic remote operations** (the broadened vision): additional typed
  integrations, PTY support for interactive human commands (`sudo`, prompts),
  and monitoring plain files via `tail -F`. To be scoped
  into milestones now that parity and e2e validation have landed. _(Host
  and per-container monitoring landed August 2026: see the dashboard cost
  budget above.)_
- **Release engineering** _(deferred to the first release)_: CI currently
  only vets the e2e package (`go vet -tags e2e`), never runs it — the
  fixture stays a local step. Running it on CI is feasible whenever it is
  wanted: `ubuntu-latest` ships Docker and allows privileged containers, at
  the cost of a couple of minutes per run and some flakiness risk. Decide
  when cutting the first release, along with whatever else that needs
  (build matrix, artifacts, versioning).

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

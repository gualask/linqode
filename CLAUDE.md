# CLAUDE.md

Linqode is a Go TUI that connects to remote servers over SSH to monitor and
operate what runs there — Docker Compose status, structured JSONL log
analysis, remote commands and scripts. Agentless: nothing is installed on
the server.

Documentation lives in `docs/`; the top-level `README.md` is the user-facing
intro (install, configuration, keys):

- **`docs/PROJECT.md`** — read first: vision, MVP scope, settled policy
  decisions, roadmap, prior art. Do not re-litigate the "Decided policies"
  section.
- **`docs/architecture.md`** — the shape of the codebase: packages,
  dependency direction, and how a session starts. Short by design; the
  behaviour is documented by capability:
  - **`docs/monitoring.md`** — what is read off the host, on which of the
    three cadence tiers, and what each reading cost when it was measured.
    Read before adding a reading: nothing joins the always-on tier without a
    measurement.
  - **`docs/interface.md`** — the screen: panels, focus, the band, the
    system view, the events feed, and what gives way when the terminal runs
    short.
  - **`docs/operations.md`** — compose lifecycle, configured scripts,
    ad-hoc commands, log following, and the machine interface.
- **`docs/tests.md`** — testing strategy, layers, conventions for new tests,
  known gaps. Includes "Looking at the UI": a change to how the interface
  looks is not finished until a frame has been rendered in color and
  looked at, because the tests run without a TTY and cannot see color at
  all.
- **`docs/docker-setup.md`** — getting a Docker engine for the e2e fixture
  (Colima on macOS), lifecycle and disk cleanup. Not needed for
  `go test ./...`.
- **`docs/porting.md`** — historical: how this codebase was ported from the
  Rust MVP (kept at the `rust-mvp` tag), including the deliberate
  divergences from it.

## Conventions

- All documentation, code comments, commit messages, and generated reports
  are written in **English**, regardless of the conversation language.
- One Go module (`github.com/gualask/linqode`): packages under `internal/`
  (`cli`, `operations`, `config`, `remote`, `probe`, `compose`, `host`,
  `logs`, `tui`)
  plus `cmd/linqode`. See `docs/architecture.md` for package ownership and
  dependency direction.
- Stack: Bubble Tea + Lipgloss, `golang.org/x/crypto/ssh`,
  `kevinburke/ssh_config`, `pelletier/go-toml/v2`, stdlib elsewhere;
  `gliderlabs/ssh` in tests only. Prefer these over alternatives unless a
  real blocker shows up.

## Build & test

```bash
go build ./...
go test ./...        # everything runs offline, no Docker needed
go vet ./...
staticcheck ./...    # CI runs it; keep it warning-free
```

Never test against a real remote server unless the user explicitly provides
one.

## Status

The MVP feature set is fully ported to Go (July 2026), the offline suite is
green, and the `tests/fixture/` sshd + docker-in-docker fixture validates
connect → ps → logs → actions against a live Docker daemon (August 2026,
behind the `e2e` build tag). The two MVP gaps the port left open — restart
counts, ad-hoc commands from the TUI (`!`) — closed in August 2026.

The dashboard landed in September 2026: focusable panels with detail views,
one sampler owning three cadence tiers, the daemon watched rather than
polled, container counters read off the kernel instead of asked of the
daemon, and the host readings that follow from a batch where a reading costs
bytes rather than a round trip. See `docs/monitoring.md` and
`docs/interface.md`.

The connect-time capability probe landed in September 2026 (`internal/probe`):
one round trip establishing docker, the socket as this user, which compose,
and whether `compose_dir` is there — and a screen built around what is left
rather than around what is missing, so a host without docker keeps its meters
and becomes a machine monitor. See `docs/monitoring.md` and
`docs/interface.md`.

The fixture is still a controlled Alpine environment: **no real deployment
has been touched yet**, and it has no temperature sensor and no GPU, so those
two readings are unit-tested against documented interfaces and e2e-tested
only for behaving correctly when absent. The same holds for three of the
probe's four findings: a host without docker, one with compose v1 and an
account outside the `docker` group are three different machines. Next per
`docs/PROJECT.md`: hardening against real hosts, then the broadened
remote-operations scope (typed integrations, PTY, `tail -F`).

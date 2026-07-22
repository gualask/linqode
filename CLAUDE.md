# CLAUDE.md

Linqode is a TUI that connects to remote servers over SSH to monitor and
operate what runs there — Docker Compose status, structured JSONL log
analysis, remote commands and scripts. Agentless: nothing is installed on the
server.

**The project is being ported from Rust to Go** (decided July 2026, see
`docs/PROJECT.md` and `docs/porting.md`). The port is feature-driven, not a
1:1 translation: the Rust tree is a reference to consult, not a contract.
It is removed once the Go port covers the MVP feature set (milestone G5;
kept at the `rust-mvp` tag).

Documentation lives in `docs/`; the top-level `README.md` is the user-facing
intro (install, configuration, keys):

- **`docs/PROJECT.md`** — read first: vision, MVP scope, settled policy
  decisions, roadmap, prior art. Do not re-litigate the "Decided policies"
  section.
- **`docs/porting.md`** — the Go porting plan: stack mapping, milestones
  G0–G5, ground rules, risks.
- **`docs/architecture.md`** — components, workspace layout, and the main
  flows (connect, status, log follow, structured logs, actions). Describes
  the Rust reference implementation until G5; flows and policies carry over.
- **`docs/tests.md`** — testing strategy, layers, conventions for new tests,
  known gaps. Same caveat: describes the Rust suite; the Go port reuses its
  cases and fixtures where the behavior carries over.

## Conventions

- All documentation, code comments, commit messages, and generated reports are
  written in **English**, regardless of the conversation language.
- **Go code** (the port): one module, packages under `internal/`
  (`remote`, `compose`, `logs`, `tui`) plus `cmd/linqode`. Stack: Bubble Tea +
  Bubbles + Lipgloss, `golang.org/x/crypto/ssh`, `kevinburke/ssh_config`,
  `pelletier/go-toml/v2`, stdlib elsewhere. Prefer these over alternatives
  unless a real blocker shows up.
- **Rust code** (the reference, until G5): edition 2024; cargo workspace with
  crates `linqode-ssh`, `linqode-logs`, `linqode-compose`, `linqode-tui`,
  `linqode-cli`. Consult it, don't extend it — new work happens in Go.

## Build & test

```bash
# Go port (once G0 lands)
go build ./... && go test ./...   # everything runs offline, no Docker needed
go vet ./...

# Rust reference (until removed at G5)
cargo test
cargo clippy --all-targets        # keep it warning-free
```

Never test against a real remote server unless the user explicitly provides
one.

## Status

The Rust MVP (M1–M5) is complete but was never exercised against a real
server. The Go port is the current phase: feature-driven milestones G0–G5
in `docs/porting.md`, reusing the Rust tests' cases and fixtures where the
behavior carries over. End-to-end validation (`tests/fixture/` sshd +
docker-in-docker) happens after the port, on the Go binary.

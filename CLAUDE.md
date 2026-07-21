# CLAUDE.md

Linqode is a Rust TUI that connects to remote servers over SSH to monitor and
manage Docker Compose deployments (service status, structured JSONL log
analysis, running commands). Agentless: nothing is installed on the server.

Documentation lives in `docs/`; the top-level `README.md` is the user-facing
intro (install, configuration, keys):

- **`docs/PROJECT.md`** — read first: vision, MVP scope, settled policy
  decisions, roadmap, prior art. Do not re-litigate the "Decided policies"
  section.
- **`docs/architecture.md`** — components, workspace layout, and the main
  flows (connect, status, log follow, structured logs, actions).
- **`docs/tests.md`** — testing strategy, layers, conventions for new tests,
  known gaps.

## Conventions

- All documentation, code comments, commit messages, and generated reports are
  written in **English**, regardless of the conversation language.
- Rust edition 2024; cargo workspace with small focused crates (`linqode-ssh`,
  `linqode-logs`, `linqode-compose`, `linqode-tui`, `linqode-cli`).
- Key crates: ratatui + crossterm, russh, tokio, serde/serde_json, toml, clap,
  thiserror/anyhow. Prefer these over alternatives unless a real blocker shows up.

## Build & test

```bash
cargo build
cargo test                 # everything runs offline, no Docker needed
cargo clippy --all-targets # keep it warning-free
```

Never test against a real remote server unless the user explicitly provides
one.

## Status

All MVP milestones (M1–M5) are implemented (July 2026) but not yet exercised
against a real server; the roadmap in `docs/PROJECT.md` tracks what each
milestone delivered and what comes next (the `tests/fixture/` sshd +
docker-in-docker fixture for end-to-end validation, then hardening).

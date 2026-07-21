# CLAUDE.md

Linqode is a Rust TUI that connects to remote servers over SSH to monitor and
manage Docker Compose deployments (service status, structured JSONL log
analysis, running commands). Agentless: nothing is installed on the server.

**Read `docs/PROJECT.md` first.** It contains the vision, MVP scope,
architecture, settled policy decisions (auth, host keys, config format,
testing strategy), the workspace layout, the M1–M5 roadmap, and prior-art
references. Do not re-litigate the "Decided policies" section.

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
cargo test                 # unit tests, no network needed
cargo clippy --all-targets # keep it warning-free
```

Integration tests (once `tests/fixture/` exists) require Docker running
locally; they spin up an sshd + docker-in-docker fixture. Never test against a
real remote server unless the user explicitly provides one.

## Status

Rebooted from scratch in July 2026 (the previous static-analysis codebase was
removed, history reset). Current milestone: **M1 — SSH connect + one-shot
remote command in a minimal ratatui screen.**

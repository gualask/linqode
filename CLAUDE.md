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

Testing is documented in `docs/tests.md` (layers, the in-process SSH server
fixture, conventions for new tests, known gaps). Everything in `cargo test`
runs offline. Integration tests against real Docker (once `tests/fixture/`
exists) require Docker running locally; they spin up an sshd +
docker-in-docker fixture. Never test against a real remote server unless the
user explicitly provides one.

## Status

Rebooted from scratch in July 2026 (the previous static-analysis codebase was
removed, history reset). Implemented so far: M1 (SSH connect + one-shot
remote command; raw screen kept behind `--exec`), M2 (compose status view:
`linqode-compose` parses `docker compose ps --all --format json`, service
table with selection and manual + 5s auto refresh), and M3 (log follow:
streaming `exec_stream` in `linqode-ssh`, the `linqode-logs` crate — line
assembly, tail buffer, search — and a log view with follow mode and `/`
search; Enter on a service opens it), M4 (structured logs: JSONL
records, `key=value` field filters, live aggregations and the `LogStore`
in `linqode-logs`; auto-detected structured rendering, `f` filter, `a`
stats panel, `t` top field in the log view), and M5 (actions: `R`/`s`/`S`
restart/stop/start the selected service, `x` runs a predefined script
from config; output streams into the shared follow view). The SSH layer
(connect, host keys, auth, exec, streaming, cancel) is covered by
in-process russh-server tests in `crates/linqode-ssh/tests/` — no Docker
needed; the `docker compose` side still awaits the sshd fixture for
end-to-end coverage. All MVP milestones (M1–M5) are implemented; next up:
the `tests/fixture/` docker-in-docker sshd fixture for end-to-end
validation, then hardening against real deployments.

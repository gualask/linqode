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
- **`docs/architecture.md`** — components, package layout, and the main
  flows (connect, status, log follow, structured logs, actions).
- **`docs/tests.md`** — testing strategy, layers, conventions for new tests,
  known gaps.
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
  (`cli`, `operations`, `config`, `remote`, `compose`, `host`, `logs`, `tui`)
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
counts in the status view, ad-hoc commands from the TUI (`!`) — closed in
August 2026. The fixture is still a controlled Alpine environment: **no
real deployment has been touched yet**. Next per the roadmap in
`docs/PROJECT.md`: hardening against real hosts, then the broadened
remote-operations scope (PTY, `.sh` upload, `tail -F`).

# Go Porting Plan

_Last updated: 2026-07-22_

Why the port happens is settled in
[PROJECT.md → Decided policies](PROJECT.md#decided-policies); this document is
the how. The port is **feature-driven, not a translation**: the product has no
users yet, so there is no compatibility burden — each product feature is
built in Go the way Go builds it best. The Rust MVP is the **reference
implementation** in a precise sense: a quarry of settled answers (auth
ladder, TOFU flow, `compose ps` parsing shapes, line-assembly edge cases,
filter semantics, test fixtures) to consult so hard problems are not solved
twice — not a contract to replicate. When a simpler design serves a feature
better, take it.

What *is* fixed: the decided policies in PROJECT.md (auth, host keys,
config format) and the user-facing surface documented in the README (CLI,
config, keybindings) — change those only deliberately, updating the docs in
the same change. Everything else — internal structure, view architecture,
data flow — is free.

## Ground rules

- **Tag first, then replace.** Before G0 lands, tag the current tree as
  `rust-mvp`. The Go port is developed in-place on `main`; the cargo
  workspace is removed when the MVP feature set is covered, not before, so
  the reference stays greppable during the port.
- **Tests capture product behavior, not code shape.** Reuse the Rust suite's
  cases and fixtures where the behavior carries over (captured `compose ps`
  output, hostile quoting inputs, chunk-boundary samples — they encode
  hard-won knowledge); write Go-native tests where the design diverges. A
  milestone is done when `go test ./...` is green offline, keeping the same
  hermeticity rules (temp dirs, loopback only, no `$HOME`, parallel-safe).
- **Divergence is allowed, silence is not.** Simplifying or redesigning
  relative to the reference is welcome; when it changes something a doc or
  the reference tests assert, record it under "Deliberate divergences" so
  the docs stay truthful.
- **Docs follow the code.** `architecture.md` and `tests.md` are rewritten
  for the Go tree once the MVP feature set is covered; until then they
  describe the reference implementation and carry a banner saying so.

## Stack and layout

Module `github.com/gualask/linqode`, one binary:

```
cmd/linqode/          # entry point: flags, wiring (was linqode-cli)
internal/config/      # config.toml loading + host selection (was in linqode-cli)
internal/remote/      # SSH: targets, connect, host keys, auth, exec (was linqode-ssh)
internal/compose/     # command builders + ps/json parsing (was linqode-compose)
internal/logs/        # line assembly, tail buffer, JSONL, filters, aggs (was linqode-logs)
internal/tui/         # Bubble Tea models, views, keymaps (was linqode-tui)
```

| Concern | Rust (reference) | Go |
| ------- | ---------------- | -- |
| TUI | ratatui + crossterm | Bubble Tea + Bubbles + Lipgloss |
| SSH client | russh | `golang.org/x/crypto/ssh` |
| known_hosts / TOFU | manual over russh | `x/crypto/ssh/knownhosts` + append-on-accept |
| SSH agent | russh agent client | `x/crypto/ssh/agent` |
| `~/.ssh/config` aliases | hand-rolled parser | `github.com/kevinburke/ssh_config` |
| Concurrency | tokio tasks + channels | goroutines + channels, `context.Context` for cancellation |
| JSON / JSONL | serde_json | stdlib `encoding/json` (revisit only if profiling demands) |
| Config (TOML) | toml + serde | `github.com/pelletier/go-toml/v2` |
| CLI args | clap | stdlib `flag` (surface is tiny: `linqode [host] --exec --config`) |
| Errors | thiserror/anyhow | stdlib `errors` wrapping; sentinel errors where the TUI branches on them |
| Test SSH server | scripted russh server | scripted `gliderlabs/ssh` server on loopback |
| Lint | clippy | `go vet` + `staticcheck` |

## Milestones

One milestone per product feature. The order below is the default (each
feature builds on the previous), but milestones may merge, split, or be
reshaped if a simpler path shows up — the gate for finishing the port is
covering the MVP feature set in PROJECT.md, not matching the Rust code.

### G0 — scaffolding

Module, package skeleton, CI (`go build`, `go vet`, `staticcheck`,
`go test`). First feature slice: config loading and host selection —
documented TOML format, `[user@]host[:port]` spec parsing,
`~/.ssh/config` resolution via `kevinburke/ssh_config`. The Rust unit tests
enumerate the edge cases worth keeping (IPv6, last-`@` rule, precedence,
identity-file discovery); the library may make some of them moot — drop
those rather than re-implement around them.

### G1 — connect and remote exec

The feature: reach a server exactly the way plain `ssh user@host` does, and
run commands on it. Decided policies apply as written — known_hosts check
with TOFU prompt and persist, refuse on mismatch (never bypassable); agent
first, then default identities, passphrase prompt only for encrypted keys.
One-shot exec (stdout/stderr/exit) and streaming exec with cancellation via
`context`; `--exec` raw-output screen in a minimal Bubble Tea program.
Testability knobs (`known_hosts_file`, `identities_only`) stay user-facing,
OpenSSH-style.

Integration tests against a scripted `gliderlabs/ssh` server on loopback
cover the same product guarantees the Rust suite proves: TOFU
once-then-silent, rejected prompt, changed-key refusal without prompting,
auth failure, exec aggregation, stream events, cancel ends a follower.

### G2 — compose status view

See the state of a compose project at a glance: `ps --format json` parsing
(both NDJSON and legacy-array shapes, null `Publishers` — reuse the captured
fixtures), shell-quoted command builders, table with state/health coloring,
selection preserved across refreshes, manual (`r`) + 5 s auto refresh,
failed refresh shows the error while the last good table stays.

### G3 — log following

Tail a service's logs live: line assembly (chunk boundaries, CRLF, UTF-8
split mid-rune — Go strings make this a distinct bug class, keep those test
cases), bounded tail buffer, follow mode with scrollback and `/` search,
bounded per-tick drain so bursts cannot starve input, cancellation on
close.

### G4 — structured log analysis

The JSONL engine: records with flattened dotted field paths, well-known
level/message/timestamp key variants, `key=value` / `key!=value` filters,
live aggregations (level counts, top values of a chosen field),
structured-rendering auto-detection with manual override, stats side panel.
The Rust engine's internals (incremental aggregation symmetric under buffer
drops) are one proven design — a simpler recompute-on-change approach is
acceptable if it holds up at realistic log rates.

### G5 — actions and scripts

Operate, not just watch: restart/stop/start the selected service and run
predefined scripts from the config, streamed through the follow view with
exit code and a status refresh on return.

**MVP coverage reached** → remove the cargo workspace, rewrite
`architecture.md`/`tests.md`/`README.md` for the Go tree, update
`CLAUDE.md`. The Rust reference stays at the `rust-mvp` tag.

## After MVP coverage

See [PROJECT.md → Roadmap](PROJECT.md#roadmap): the e2e Docker fixture
(unchanged plan, now validating the Go binary), then the broadened
remote-operations scope. The fixture was deliberately deferred past the
port so real-server validation is paid once, on the Go implementation.

## Known risks

- **known_hosts TOFU**: `knownhosts` verifies but does not append; the
  accept-and-persist path (hashed vs plain entries, `[host]:port` syntax) is
  hand-written — port the Rust tests for it and cover hashed entries.
- **UTF-8 handling**: Rust's `&str` invariants caught split-rune bugs at
  compile time; in Go they only surface at runtime. The G3 assembler tests
  are the safety net.
- **Bubble Tea architecture**: Elm-style update loop vs ratatui's
  immediate-mode draw. Views port at the behavior level (state machines,
  keymaps), not line-by-line.
- **Blocking prompts**: the Rust code used `block_in_place` for passphrase /
  TOFU prompts mid-connect; in Bubble Tea these become messages to the
  update loop instead — the one place where control flow genuinely differs.

## Deliberate divergences

None yet. Record any here with rationale.

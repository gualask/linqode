# Testing

_Last updated: 2026-07-23_

How Linqode is tested, what each layer covers, and how to extend it. The
strategy in short: pure logic is unit-tested against captured fixtures with
no network; the SSH client is exercised for real against a scripted
in-process server on loopback; the TUI's view models are tested directly as
pure update/view functions; full end-to-end coverage (connect → ps → logs →
exec against a live Docker) is the job of the planned `tests/fixture/`
container, so any developer — human or LLM — can verify changes locally
without access to a real server.

## Running

```bash
go test ./...              # everything below except the Docker fixture
go vet ./...
staticcheck ./...          # keep it warning-free (CI runs it)
```

Everything runs offline: no Docker, no network beyond loopback, no state
outside per-test temp directories. Tests are parallel-safe; the config
tests fake `$HOME` per test via `t.Setenv`.

## Layers

### 1. Unit tests (colocated `_test.go` files)

Pure logic tested against small fixtures. Many cases and fixtures are
inherited from the Rust reference suite (tag `rust-mvp`), which had
hardened them first:

| Package | What is covered |
| ------- | --------------- |
| `internal/config` | Config parsing (documented format, tolerance of future sections, missing-`host` rejection, scripts sorted by name), host selection rules, default-path loading |
| `internal/remote` | `[user@]host[:port]` spec parsing (IPv6, last-`@` rule, rejects incl. port 0), `~/.ssh/config` alias resolution and precedence, identity-file discovery limited to existing files, tilde expansion |
| `internal/compose` | Command builders (`ps`, `logs`, actions) incl. shell quoting of hostile paths; `ps --format json` parsing in both shapes (NDJSON ≥ 2.21, legacy array), null `Publishers`, sorting; port summaries collapsing IPv4/IPv6 duplicates |
| `internal/logs` | Line assembly across arbitrary chunk boundaries (CRLF, invalid UTF-8, runes split mid-chunk); ring-buffer drop accounting; ASCII-case-insensitive search on rune-safe offsets; JSONL record parsing (flattening, numeric literals verbatim, well-known keys); filter parsing and matching (AND terms, negation, case folding); the filtered visible view across buffer drops; wrap-around search over the visible view; detection heuristic; stats recompute incl. a test pinning that counts follow drops |

### 2. In-process SSH integration tests (`internal/remote/*_test.go`, package `remote_test`)

The production client exercised against a scripted `gliderlabs/ssh` server
running in the same process, on an ephemeral loopback port. This layer
verifies the connect/exec plumbing for real — handshake, host-key policy,
auth, exec, streaming — without any external dependency.

The fixture (`testserver_test.go`) generates an ed25519 host key and one
authorized client key per test in a temp dir, maps exact command strings to
scripted replies (stdout/stderr chunks, exit code, `holdOpen` to emulate a
follower), and rejects everything but user `linqode-test` with the one
accepted key. Scripted prompters count how often they are consulted.

Hermeticity comes from two production `ConnectOptions` knobs that
deliberately mirror OpenSSH options usable by end users: `KnownHostsFile`
(like `UserKnownHostsFile`; tests point it at a temp file) and
`IdentitiesOnly` (so the developer's real agent keys are never offered).
Every potentially-blocking wait sits under a 10-second guard timeout so a
regression hangs the test, not CI.

| Test | Proves |
| ---- | ------ |
| `TestTOFUAcceptsPersistsAndReconnectsSilently` | TOFU prompts exactly once, persists the normalized `[host]:port`, reconnect is silent; exec returns scripted stdout + exit 0 |
| `TestRefusedHostKeyAbortsConnect` | Declining the prompt yields `HostKeyRejectedError` |
| `TestChangedHostKeyRefusesWithoutPrompting` | A pinned different key yields `HostKeyChangedError` with the conflicting line, without ever consulting the prompter (anti-MITM) |
| `TestUnauthorizedKeyFailsAuth` | An unaccepted identity yields `AuthFailedError` |
| `TestEncryptedKeyAsksPassphrase` | The right passphrase gets in after one prompt; a wrong one retries 3× then yields `BadPassphraseError` |
| `TestExecCollectsStdoutStderrAndExitCode` | One-shot exec aggregates multi-chunk stdout, stderr, and the exit code |
| `TestExecStreamDeliversEventsThenEnds` | Streaming exec delivers stdout/stderr/exit events, then the channel closes |
| `TestExecStreamCancelEndsAFollower` | A `holdOpen` follower streams while running; cancelling the context ends the event stream instead of hanging |

### 3. View-model tests (`internal/tui/*_test.go`)

Bubble Tea models are pure update/view functions, so the view logic that
stayed untested in the Rust reference is covered directly here: selection
preservation across refreshes, error handling that keeps the last good
table, follow/scroll transitions, search cycling with wrap-around, the
bounded burst drain, filter commit/clear/errors, structured-rendering
detection and override, the stats panel, action key routing, and the
scripts menu cycle. The log view is fed through a hand-built `LogFeed`
channel — no SSH involved.

### 4. Docker fixture (planned, `tests/fixture/`)

The plan of record: a docker-compose fixture running sshd + docker-in-docker
with a demo compose project emitting plain-text and JSONL logs, connected
to over real SSH for full-path coverage (connect → ps → logs → exec). This
is the only layer that will validate the `docker compose` side end-to-end;
no milestone has been exercised against a real Docker host until it lands.

## Conventions for new tests

- Default to the lowest layer that can catch the regression: parsing and
  state-machine logic as unit tests; anything touching the SSH transport
  against the in-process fixture server; view behavior as view-model
  tests.
- Keep tests hermetic: temp dirs for every file, `server.options()` for
  every connect, `t.Setenv` for `$HOME`, no reliance on the user's agent
  or test order.
- Wrap potentially-blocking waits in guard timeouts (`guardTimeout`).
- Test-only code lives in `_test.go` files; the SSH fixture stays in
  `testserver_test.go` within package `remote_test`, so nothing test-only
  can leak into production builds.

## Known gaps

- **Full-screen rendering**: view tests assert on the rendered strings,
  not on terminal placement/ANSI details; the app-model routing between
  views has only indirect coverage.
- **Remote-process termination semantics** of the cancel path (SIGTERM
  delivery / SIGPIPE fallback depend on the real sshd and OS).
- **SSH agent auth** (skipped via `IdentitiesOnly`; needs a fake agent
  socket or the e2e fixture).
- **`cmd/linqode` wiring** (`startLogFeed`, `fetchServices`, flag
  handling) has no direct tests, though all its pieces do.
- **Docker fixture**: see above — the plan of record for end-to-end.

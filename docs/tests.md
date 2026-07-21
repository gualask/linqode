# Testing

_Last updated: 2026-07-21_

How Linqode is tested, what each layer covers, and how to extend it. The
strategy in short: pure logic is unit-tested against captured fixtures with
no network; the SSH client is exercised for real against a scripted
in-process server on loopback; full end-to-end coverage (connect → ps →
logs → exec against a live Docker) is the job of the planned
`tests/fixture/` container, so any developer — human or LLM — can verify
changes locally without access to a real server.

## Running

```bash
cargo test                    # everything below except the Docker fixture
cargo test -p linqode-ssh     # unit + in-process SSH integration tests
cargo test -p linqode-logs    # log engine only
cargo clippy --all-targets    # lints test code too; keep it warning-free
```

Everything runs offline: no Docker, no network beyond loopback, no state
outside per-test temp directories. Tests are parallel-safe.

## Layers

### 1. Unit tests (in each crate, `#[cfg(test)]` modules)

Pure logic tested against small fixtures, colocated with the code:

| Crate | What is covered |
| ----- | --------------- |
| `linqode-ssh` | `[user@]host[:port]` spec parsing (IPv6, last-`@` rule, rejects), `~/.ssh/config` alias resolution and precedence, default identity-file discovery |
| `linqode-compose` | Command builders (`ps`, `logs`, `restart`/`stop`/`start` actions) incl. shell quoting of hostile paths; `docker compose ps --format json` parsing in both shapes (NDJSON ≥ 2.21, legacy array), null `Publishers`, sorting; port summaries collapsing IPv4/IPv6 duplicates |
| `linqode-logs` | Line assembly across arbitrary chunk boundaries (CRLF, UTF-8 split mid-character, unterminated tail); tail buffer overflow accounting; ASCII-case-insensitive find with char-boundary guarantees; JSONL record parsing (nested-object flattening, non-object rejection, well-known level/message/time keys); filter expression parsing and matching (AND terms, negation, case folding); aggregation add/remove symmetry, level counts, top values; `LogStore` — filtered-view indexing across buffer drops, wrap-around search over the visible view, JSONL detection heuristic |
| `linqode-cli` | Config file parsing (documented format, `scripts` tables sorted by name, tolerance of future sections), host selection rules |

Captured `docker compose` output lives inline in the test modules today
(e.g. `parse.rs` carries an NDJSON sample from compose v2.27). If fixtures
grow, move them to `crates/linqode-compose/tests/data/`.

### 2. In-process SSH integration tests (`crates/linqode-ssh/tests/`)

The production client exercised against a scripted SSH server running in
the same process, on an ephemeral loopback port. This is the layer that
verifies the M1/M3 plumbing for real — handshake, host-key policy, auth,
exec, streaming — without any external dependency.

```
crates/linqode-ssh/
  src/            # production code — nothing in here is test-specific
  tests/          # compiled ONLY into test binaries, never into the library
    support/
      mod.rs      # the fixture: scripted russh server + test prompters
    e2e.rs        # the tests
```

**Separation rule.** The `tests/` directory is the boundary: Cargo compiles
it exclusively into integration-test binaries, so nothing under it can leak
into production builds. Test-only helpers go in `tests/support/`; the
production `src/` tree must not contain code that exists only for tests.

The one production accommodation is `ConnectOptions` (in `src/session.rs`),
and it is deliberately *not* test-specific — each knob mirrors an OpenSSH
option and is usable by end users:

- `known_hosts_file: Option<PathBuf>` — like `UserKnownHostsFile`. Tests
  point it at a temp file so they never touch the real `~/.ssh/known_hosts`.
- `identities_only: bool` — like `IdentitiesOnly`. Tests set it so the
  developer's real SSH agent keys are never offered to the fixture server.

`Session::connect` keeps its plain signature and applies the defaults;
`Session::connect_with` takes explicit options.

#### The fixture (`tests/support/mod.rs`)

- **`TestServer::spawn(scripts)`** binds `127.0.0.1:0`, generates an
  ed25519 host key and one authorized client key (written to a temp dir
  together with an empty `known_hosts`), and serves connections until the
  test's tokio runtime shuts down. `server.target()` / `server.options()`
  return a ready-made `Target` and hermetic `ConnectOptions`.
- **`Script`** maps an exact command string to a reply: stdout/stderr
  chunks, exit code, and `hold_open` to emulate a follower (`logs -f`) that
  only ends when the client cancels. Unknown commands get a channel failure.
- **Auth model**: exactly one accepted key pair for user `linqode-test`;
  anything else is rejected (with zero rejection delay, so failure-path
  tests stay fast).
- **Prompters**: `AcceptHostKey` (accepts and counts prompts),
  `RejectHostKey`, and `NoInteraction` (panics if consulted — used to prove
  a path must not prompt).

#### The tests (`tests/e2e.rs`)

Every test spawns its own server with its own scripts and temp dir, and
wraps waits in a 10 s guard timeout so a regression hangs the test, not CI.

| Test | Proves |
| ---- | ------ |
| `tofu_accepts_persists_and_reconnects_silently` | TOFU prompts exactly once, persists `[host]:port` to the injected `known_hosts`, reconnect is silent; exec returns scripted stdout + exit 0 |
| `refused_host_key_aborts_connect` | Declining the prompt yields `Error::HostKeyRejected` |
| `changed_host_key_refuses_connect` | A pinned different key yields `Error::HostKeyChanged { line }` without ever consulting the prompter (anti-MITM) |
| `unauthorized_key_fails_auth` | An unaccepted identity yields `Error::AuthFailed` |
| `exec_collects_stdout_stderr_and_exit_code` | One-shot exec aggregates multi-chunk stdout, stderr (ext=1), and the exit code |
| `exec_stream_delivers_events_then_ends` | Streaming exec delivers `Stdout`/`Stderr`/`Exit` events, then the event channel closes |
| `exec_stream_cancel_ends_a_follower` | A `hold_open` follower streams while running; `cancel()` ends the event stream instead of hanging |

#### What this layer does not cover

- The remote `docker compose` CLI itself (output shapes are covered by
  `linqode-compose` unit fixtures, but not a live compose).
- Remote-process termination semantics of the cancel path (SIGTERM
  delivery / SIGPIPE fallback depend on the real sshd and OS).
- Encrypted-key passphrase prompting against a real key file, and SSH
  agent auth (skipped via `identities_only`).
- TUI rendering and key handling (no automated coverage yet; see Gaps).

### 3. Docker fixture (planned, `tests/fixture/`)

The plan of record: a docker-compose fixture running sshd + docker-in-docker
with a demo compose project emitting plain-text and JSONL logs, connected
to over real SSH for full-path coverage (connect → ps → logs → exec). Not
yet built — Docker is unavailable on the current dev machine. This is the
only layer that will validate the `docker compose` side end-to-end;
milestones M1–M3 remain unverified against a real Docker host until then.

## Conventions for new tests

- Default to the lowest layer that can catch the regression: parsing and
  state-machine logic as unit tests; anything touching the SSH transport in
  `linqode-ssh/tests/e2e.rs` against the fixture server.
- Keep tests hermetic: temp dirs for every file, `server.options()` for
  every connect, no reliance on `$HOME`, the user's agent, or test order.
- Async tests that call `Session::connect*` need
  `#[tokio::test(flavor = "multi_thread")]` (the prompt path uses
  `block_in_place`).
- Wrap potentially-blocking awaits in `tokio::time::timeout` guards.
- Test-only code is marked by location (`tests/`), not by naming; if
  another crate ever needs the SSH fixture, promote `tests/support/` to a
  dedicated dev-dependency crate rather than exporting it from the library.

## Known gaps

- **TUI**: view logic (scroll/follow/search state, selection preservation)
  and rendering are untested. Plan: extract view structs behind plain
  method calls (done for `StatusView`/`LogView`) and add ratatui
  `TestBackend` snapshot-style tests; feed `LogView` via a hand-built
  `LogFeed` channel.
- **`Remote` trait**: `exec_stream` is not part of the trait yet, so the
  CLI's log-feed pump cannot be tested against a fake without SSH.
- **CLI wiring**: `fetch_services` / `start_log_feed` / `exec_failure`
  have no direct tests (their pieces do).
- **Docker fixture**: see above — the plan of record for end-to-end.

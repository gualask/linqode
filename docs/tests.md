# Testing

_Last updated: 2026-10-05._

How Linqode is tested, what each layer covers, and how to extend it. The
strategy in short: pure logic is unit-tested against captured fixtures with
no network; the SSH client is exercised for real against a scripted
in-process server on loopback; the TUI's view models are tested directly as
pure update/view functions; the machine adapter and shared operations have
contract tests; full end-to-end coverage against live SSH and Docker is the
job of the `tests/fixture/` container. Any developer — human or LLM — can
therefore verify changes locally without access to a real server.

## Running

```bash
go test ./...              # everything below except the Docker fixture
go vet ./...
staticcheck ./...          # keep it warning-free (CI runs it)

go test -tags e2e -timeout 20m ./tests/e2e/   # the Docker fixture
```

Everything except the Docker fixture runs offline: no Docker, no network
beyond loopback, no state outside per-test temp directories. Tests are
parallel-safe; the config tests fake `$HOME` per test via `t.Setenv`. The
fixture is the deliberate exception — it needs a Docker engine and pulls
images — which is why it is tagged out of the default run.

## Layers

### 1. Unit tests (colocated `_test.go` files)

Pure logic tested against small fixtures. Many cases and fixtures are
inherited from the Rust reference suite (tag `rust-mvp`), which had
hardened them first:

| Package | What is covered |
| ------- | --------------- |
| `internal/config` | Config parsing (documented format, tolerance of future sections, missing-`host` rejection, scripts sorted by name), host selection rules, default-path loading |
| `internal/cli` | Human/machine routing, exact operands and option placement, strict machine boundaries, JSON/JSONL payloads, streaming without whole-output buffering, typed failures, cancellation, and exit mapping |
| `internal/local` | The local Executor against real processes (Unix only): output and exit codes, the C locale, the home directory, cancellation ending the command and its children at every stage, `SIGKILL` for a group that ignores `SIGTERM`, a stream closing despite a descendant that left the group (skipped without `setsid`), and `Close` ending every command the session started before it returns |
| `cmd/linqode` | Composition: machine routing before connecting |
| `internal/operations` | Strict configured catalog, shared status/stats/log workflows, exact lifecycle/script selection, service validation, stream assembly/cancellation, and no mutation retry |
| `internal/remote` | `[user@]host[:port]` spec parsing (bare and bracketed IPv6 incl. `[::1]:2222`, last-`@` rule, rejects incl. port 0), `~/.ssh/config` alias resolution and precedence, identity-file discovery limited to existing files, tilde expansion; `IdentitiesOnly` read from `~/.ssh/config`, `ProxyJump`/`ProxyCommand` refused unless `none`; host key algorithms narrowed to the recorded types (RSA with its SHA-2 signatures) and a key of an unrecorded type refused by the callback itself |
| `internal/compose` | Command builders (`ps`, `logs`, actions, restart inspect) incl. shell quoting of hostile paths and container names; `ps --format json` parsing in both shapes (NDJSON ≥ 2.21, legacy array), null `Publishers`, sorting; port summaries collapsing IPv4/IPv6 duplicates; restart counts parsed leniently (leading slash stripped, a vanished container's error line skipped without losing the rest) and an absent count staying unknown rather than zero |
| `internal/host` | Metrics parsing from the marked `/proc` + `df -Pk` sections; tolerance of missing sections and of garbage (both leave fields zero rather than failing the sample); derived percentages guarding against division by zero and unsigned underflow; the command asking for every section |
| `internal/tui/spark` | The windows strips are scaled against — widened when a reading barely moves, not clamped at a hundred for a container's CPU, widened by a share of itself for an amount — and the shapes: a bar that keeps its width and never draws a share above zero as nothing, shares divided into one bar of an exact width with the smallest still drawn, a histogram that stacks across rows and leaves an empty column on the bare track, and solid columns half a cell tall at the least |
| `internal/logs` | Line assembly across arbitrary chunk boundaries (CRLF, invalid UTF-8, runes split mid-chunk); ring-buffer drop accounting; ASCII-case-insensitive search on rune-safe offsets; JSONL record parsing (flattening, numeric literals verbatim, well-known keys); filter parsing and matching (one field's values as alternatives, negation, case folding, quoting, toggling, a field's own terms set aside for its counts); the fields ranked for the stats panel; the filtered visible view across buffer drops; wrap-around search over the visible view; detection heuristic; stats recompute incl. a test pinning that counts follow drops; record timestamps read only when they carry a zone or are a plausible epoch in any of four units; the recent counts' choice of clock, the traceback placed with the record above it, the counts narrowing to the filtered view, and the window following the pace of what is in view |

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
Agent-auth tests use a temporary Unix socket serving an in-process keyring.
Cancellation tests also use a protocol-level SSH server that withholds
channel-open or exec replies, or sends output EOF without an exit status.
Every potentially-blocking wait sits under a 10-second guard timeout so a
regression hangs the test, not CI.

| Test | Proves |
| ---- | ------ |
| `TestTOFUAcceptsPersistsAndReconnectsSilently` | TOFU prompts exactly once, persists the normalized `[host]:port`, reconnect is silent; exec returns scripted stdout + exit 0 |
| `TestRefusedHostKeyAbortsConnect` | Declining the prompt yields `HostKeyRejectedError` |
| `TestNonInteractiveUnknownHostKeyFailsWithoutLearning` | Machine authentication refuses an unknown key without prompting or changing `known_hosts` |
| `TestChangedHostKeyRefusesWithoutPrompting` | A pinned different key yields `HostKeyChangedError` with the conflicting line, without ever consulting the prompter (anti-MITM) |
| `TestKnownKeyOfAnotherTypeIsNotAChangedKey` | A host offering ECDSA beside the pinned ed25519 key is verified with the pinned key, not refused as changed |
| `TestChangedKeyOfRecordedTypeIsRefusedAmongOthers` | Narrowing the algorithms does not weaken the refusal: a different key of a recorded type is still `HostKeyChangedError` |
| `TestOnlyUnrecordedKeyTypeIsRefused` | A host that can show no recorded type yields `HostKeyTypeNotRecordedError`, never a TOFU prompt |
| `TestHostCertificateFromKnownAuthority` | A host certificate signed by a `@cert-authority` in `known_hosts` is accepted without a prompt |
| `TestUnauthorizedKeyFailsAuth` | An unaccepted identity yields `AuthFailedError` |
| `TestAuthFallsBackToLaterIdentity` | Missing, invalid, rejected, or skipped keys do not hide a later authorized identity |
| `TestNonInteractiveSkipsEncryptedIdentity` | Machine mode passes over an encrypted identity to a later plain one, and reports `PassphraseRequiredError` only when nothing else is accepted |
| `TestIdentitiesOnlyKeepsUnconfiguredAgentKeysBack` | With `IdentitiesOnly`, an authorized key the agent holds for nothing configured is never offered |
| `TestIdentitiesOnlyUsesAgentForConfiguredKey` | …while the agent still signs for a configured identity, recognised from an encrypted key file without a passphrase prompt |
| `TestAuthAgentFallbackAndLazyPassphrase` | An empty or unauthorized agent falls back to identity files; agent success avoids unlocking an encrypted file |
| `TestConnectCancellationInterruptsSSHHandshake` | Cancelling during handshake returns promptly instead of waiting for the SSH timeout |
| `TestCommandCancellationDuringSSHWaits` | Both exec modes honor cancellation during channel creation, exec acknowledgement, and the wait for exit after output EOF |
| `TestEncryptedKeyAsksPassphrase` | The right passphrase gets in after one prompt; a wrong one retries 3× then yields `BadPassphraseError` |
| `TestExecCollectsStdoutStderrAndExitCode` | One-shot exec aggregates multi-chunk stdout, stderr, and the exit code |
| `TestExecStreamDeliversEventsThenEnds` | Streaming exec delivers stdout/stderr/exit events, then the channel closes |
| `TestExecStreamCancelEndsAFollower` | A `holdOpen` follower streams while running; cancelling the context ends the event stream instead of hanging |

### 3. View-model tests (under `internal/tui/`)

Bubble Tea models are pure update/view functions, so the view logic that
stayed untested in the Rust reference is covered directly here: selection
preservation across refreshes, error handling that keeps the last good
table, follow/scroll transitions, search cycling with wrap-around, the
bounded burst drain, filter commit/clear/errors, the filter picked from the stats panel with a cursor that keeps its row, structured-rendering
detection and override, the stats panel, the action and script menus
(navigation, running the chosen entry, closing on esc or on the key that
opened them), the RESTARTS column appearing only once counts exist, the
system view giving the charts their rows and the processes all the rest and
keeping the processes when only one fits, share charts drawn against their
whole scale with their columns drawn before the first trend arrives, the readings in two columns when wide, both rankings side by
side with no key to switch them and no cap on the list,
a filesystem's fill time said only on enough evidence and within its reach,
the live panel opening on the trend the sampled counters built,
docker's disk drawn under the table only in rows the table leaves empty and
read only while it is drawn, the log view's counts split into what is recent
and what is all, its levels drawn as one bar worst first, its panel never
taller than the log, and the
`!` prompt (keys type instead of acting while it is open, empty input runs
nothing, esc cancels, and it reopens on the last command). Views receive
hand-built `operations.Feed` channels — no SSH involved.

Two tests pin design rules rather than behaviors. The keys Linqode invents
must not differ only by case, so `R`, `S`, `C` and `X` are asserted to do
nothing on the services table; `n`/`N` in the log view is the one exempt pair
— see [operations.md](operations.md), "Service actions". And navigation is the
arrows alone, so `j`, `k`, `g` and `G` are asserted to move nothing, which is
what keeps the removed aliases from creeping back one panel at a time.

Assertions about color compare styles, not rendered strings: tests run
without a TTY, where lipgloss drops the very colors under test. That leaves
a gap no assertion closes, which is what [Looking at the
UI](#looking-at-the-ui) is for.

### 4. Docker fixture (`tests/fixture/`, driven by `tests/e2e/`)

The only layer that validates the `docker compose` side end-to-end: a
container running sshd in front of a real docker-in-docker daemon, with a
demo project emitting plain-text and JSONL logs, reached over real SSH. It
proves the commands Linqode builds actually produce the output its parsers
expect — the one thing the layers above cannot check, since they either
stub the daemon or replay captured output.

It sits behind the `e2e` build tag, so `go test ./...` stays offline:

```bash
go test -tags e2e -timeout 20m ./tests/e2e/
```

`TestMain` generates the SSH key, brings the fixture up, waits for the demo
project, and tears it down; `-fixture.keep` leaves it running for
inspection. Setting up a Docker engine to run this — including keeping it
off when unused — is covered in [docker-setup.md](docker-setup.md); the
fixture's own layout is documented in
[tests/fixture/README.md](../tests/fixture/README.md).

The same fixture doubles as a live server for manual work:
`scripts/dev-fixture.sh run` starts the engine, brings it up, and runs the
TUI against it. The script with no command prints what it can do.

| Test | Proves |
| ---- | ------ |
| `TestComposeStatusReportsDemoProject` | `ps --all --format json` from a live daemon parses into the model: every service present, sorted by name, `running`/`exited` states, `healthy`/`unhealthy` health, published ports collapsing into the summary |
| `TestFollowPlainTextLogs` | An unstructured service streams through the line assembler as readable lines, and is not mistaken for JSONL |
| `TestFollowStructuredLogs` | JSONL written by a real container reaches the engine intact: level, message, timestamp, and nested objects flattened to dotted paths (`http.status`) |
| `TestCancelEndsFollower` | Cancelling a follow closes the stream **and** the remote `compose logs -f` process is gone — checked in the remote process list, with a sanity check that it was visible while running |
| `TestRestartServiceRestartsContainer` | The action command runs against a real project and the service comes back `running` |
| `TestTOFUPersistsHostKey` | Trust-on-first-use against a real sshd: prompts exactly once, the second connection is silent |
| `TestHostMetricsAgainstRealHost` | The header's metrics command works on a busybox userland — the `/proc` layout and `df -Pk` support that differ most from a developer's machine — and every field arrives with a sane derived percentage |
| `TestStatsStreamAgainstRealProject` | The live panel's stream yields parseable samples for the project's running containers, and only those |
| `TestStatsSampleAgainstRealProject` | The periodic refresh's one-shot form terminates on its own and parses into a whole sample, and logs what it cost — the measurement the 20 s interval rests on |
| `TestRestartCountsAgainstRealProject` | Real Docker restart counters distinguish stable, policy-restarted, and manually restarted services |
| `TestWatchReportsProjectChanges` | The event stream a restart is noticed through: the change reaches the client without anything being asked, health-check `exec_*` noise never does (the demo project probes two services every two seconds), and neither does a container started outside the project — the label filter being the only thing scoping `docker events` to this session |
| `TestWatchStopsWithItsContext` | Closing the stream ends the remote `docker events`, which would otherwise outlive every session that opened one |
| `TestContainerCgroupsAgainstRealProject` | The cgroup globs find the layout a real daemon uses, the ids in those paths reconcile with the short ones `ps` reports, the unprivileged operator account can read all of it — including `/proc/<pid>/net/dev` — and two readings a couple of seconds apart derive a plausible CPU percentage |
| `TestProbeAgainstRealHost` | The capability probe against a real daemon, in the direction it must not get wrong: an ordinary account in the `docker` group, the compose plugin and a project directory that is there must all read as "nothing to report", because a probe that turns capabilities off on a working host is worse than no probe. Also that nothing reaches stderr — every command in the batch is guarded, and a leaked guard would put "not found" in front of an operator for a reading that succeeded |
| `TestProbeFindsAMissingComposeDir` | The one probe finding this fixture can produce on demand, and the distinction the sentence turns on: the daemon is fine, only the path is wrong |
| `TestMachineBinaryCommands` | The compiled binary uses default TOML and non-interactive SSH for JSON status, bounded JSONL logs, a validated restart, an exact configured script, and exact propagation of remote exit `7` |

`tests/e2e/cost_test.go` measures what candidate dashboard commands cost on
the server. It is opt-in (`-cost.measure`) and asserts nothing; it exists so
the refresh design in [PROJECT.md](PROJECT.md) can be re-derived from
numbers when docker or the scenario changes.

Both the authorized identity and the server's host key are generated into
`.keys/` on first use and reused afterwards — never checked in. Keeping them
stable means a client that trusted the fixture once still connects after it
is recreated, which is what makes the fixture usable for driving the TUI by
hand. TOFU is still exercised for real: every test connects with its own
empty `known_hosts`, so the prompt path runs each time regardless. Delete
`.keys/` to force new keys.

## Looking at the UI

**A change to how the interface looks is not finished until someone has
looked at it in color.** This is not a suggestion born of taste: two
defects shipped precisely because nobody could. A gauge rendered its empty
track in the same saturated color as its fill, so the part meaning "unused"
shouted as loudly as the part meaning "used". A heading band was shaded
close enough to the selected row that the two read as the same thing. Both
passed every assertion, because assertions compare styles and the tests run
without a TTY where lipgloss emits no color at all.

`internal/tui/home/uishot_test.go` drives the model into the states worth
seeing — every health and state color, a selected row, the narrow layout,
the stale flag — and writes them to one page. Beside it,
`uishot_fixtures_test.go` holds the sample data and `uishot_html_test.go`
converts each frame's escape sequences to HTML:

```bash
LINQODE_UI_SHOT=/tmp/shot.html go test ./internal/tui/home/ -run TestUIShot
open /tmp/shot.html
```

Add `LINQODE_UI_SHOT_LIGHT=1` for the same frames on a light terminal. Every
colour in the palette has a light variant, and a change that only ever gets
looked at on a dark page ships its light half unseen.

Without the environment variable the test skips, so a normal run pays
nothing for it. It asserts nothing and nothing depends on its output: it is
a viewer, and the assertions stay in the tests beside it. When a view grows
a state worth inspecting, add a frame rather than a new test.

It lives in `_test.go` files, not a package of its own, because it is not
production code: anything under `internal/` is compiled by `go build ./...`
and linted as shipping code, while `_test.go` is compiled only for tests.
The stdlib does promote test support into real packages — `httptest`,
`iotest` — but that earns its place when several packages share it. With
one caller, a helper beside its test is the smaller thing. If the log view
wants frames too, copy it before promoting it: the converter is a hundred
lines, and a little duplication is cheaper than a package that ships for
nobody.

One thing it deliberately does not do: it renders a frame, not a session,
so cursor placement, the alternate screen, and resize behavior are still
unasserted (see [Known gaps](#known-gaps)).

It used to carry a second caveat — that its palette was only one plausible
terminal's, so it could not show what any given operator would see. That
stopped being true when `internal/tui/theme` moved from the ANSI slots to
named colors: the frame now says which colors it wants, and the viewer
renders those. The caveat survives only for a sixteen-color terminal, where
lipgloss degrades each value to its nearest slot and the operator's scheme
decides again.

### Why not VHS

[VHS](https://github.com/charmbracelet/vhs) is the obvious candidate — same
authors as Bubble Tea and Lipgloss, scripted `.tape` files, a real headless
terminal, GIF and PNG output. It is the right tool for a **demo
recording**, and if the README ever wants one, that is what should make it.

It is the wrong tool for this job. VHS drives the compiled binary, and
Linqode's binary does nothing without a host to connect to: seeing an
unhealthy service next to a restarting one would mean bringing up Colima,
the fixture, and the demo project, then contriving the demo project into
that state. `TestUIShot` sets the state directly, in-process, in
milliseconds, and can render conditions the fixture cannot easily produce
at all — a stale host sample, a load average above one per core. Neither
does it need a terminal, ffmpeg, or a recording to be diffed frame by
frame.

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

- **Three of the four probe findings need a different machine.** A host
  without docker, one with compose v1, and an account outside the `docker`
  group are three fixtures, and this is one. They are unit-tested against the
  CLIs' documented output — including the exact stderr a refused socket
  produces — and e2e-tested only in the direction the fixture can reach. Same
  position as temperatures and graphics cards, and it closes the same way: a
  real deployment.
- **The TUI itself is never driven end-to-end**: the e2e suite calls the
  shared production operations directly and drives the compiled binary only
  through machine commands, not through the Bubble Tea program. Keybindings
  and view routing are therefore covered by view-model tests rather than a
  synthetic terminal; terminal placement and ANSI behavior are not asserted.
  [Looking at the UI](#looking-at-the-ui) covers what a frame *looks* like,
  which is a different question from how the terminal is driven.

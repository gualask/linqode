# E2E fixture

A container that looks like a host Linqode manages: **sshd in front of a
real Docker daemon**, with a demo Compose project running inside it. It
exists so the `docker compose` side can be verified end to end — the offline
suite can only check that commands are built correctly and that captured
output parses, never that the two actually meet.

Driven by [`tests/e2e`](../e2e); see [docs/tests.md](../../docs/tests.md) for
where this sits in the testing strategy and
[docs/docker-setup.md](../../docs/docker-setup.md) for getting a Docker
engine on macOS.

## Running

```bash
go test -tags e2e -timeout 20m ./tests/e2e/
```

`TestMain` generates the SSH key, brings the container up, waits for the
demo project, and tears everything down afterwards. Nothing needs to be
started by hand.

The first run builds the image, so allow a few minutes; later runs reuse it
from the build cache. The daemon inside re-pulls `busybox` every run —
teardown is `down -v`, so nothing survives between runs. A full run takes
about a minute once the image is built.

To keep the fixture up and poke at it by hand:

```bash
go test -tags e2e -timeout 20m ./tests/e2e/ -fixture.keep
ssh -p 2222 -i tests/fixture/.keys/id_ed25519 linqode@127.0.0.1
docker compose -f tests/fixture/docker-compose.yml down -v   # when done
```

## Driving the TUI by hand

The fixture doubles as a live server to try the TUI against.
[`scripts/dev-fixture.sh`](../../scripts/dev-fixture.sh) does the whole
round trip — starts the Docker engine if it is down, brings the fixture up,
loads the identity into an agent, builds, and runs the TUI:

```bash
scripts/dev-fixture.sh          # engine -> fixture -> build -> TUI
scripts/dev-fixture.sh up       # fixture only
scripts/dev-fixture.sh ssh      # shell on the fixture
scripts/dev-fixture.sh status   # engine, fixture, demo project
scripts/dev-fixture.sh down     # tear it down (--colima stops the VM too)
```

The fixture stays up after the TUI exits, so the next run is a few seconds.
The steps by hand, equivalently:

```bash
go test -tags e2e ./tests/e2e/ -run TestTOFUPersistsHostKey -fixture.keep
ssh-add tests/fixture/.keys/id_ed25519
go build -o linqode ./cmd/linqode
./linqode --config tests/fixture/linqode.toml fixture
```

`linqode.toml` points at the fixture and defines a few scripts for the `x`
menu. The key goes into the agent because Linqode looks for identities there
and in `~/.ssh`, not in the fixture directory.

Both keys are generated once into `.keys/` and reused, so this is a one-time
setup: the fixture keeps the same identity across recreations, and the entry
your client wrote into `~/.ssh/known_hosts` stays valid. If you delete
`.keys/`, the fixture comes back as a different server and the stale entry
has to go:

```bash
ssh-keygen -R '[127.0.0.1]:2222'
ssh-add -d tests/fixture/.keys/id_ed25519
```

## What is inside

| Piece | Purpose |
| ----- | ------- |
| `Dockerfile` | `docker:29-dind` + `openssh-server`; an unprivileged `linqode` account in the `docker` group, exactly what an operator would have |
| `entrypoint.sh` | generates host keys per run (so TOFU is exercised for real), installs the authorized key, starts sshd, then hands off to the upstream dind entrypoint with dockerd as PID 1 |
| `docker-compose.yml` | runs the server privileged (required by dind), publishes sshd on `127.0.0.1:2222` |
| `project/` | the demo Compose project, started automatically once dockerd is up |

## The demo project

Its services exist to give each feature something to chew on, not to do
anything useful:

| Service | Why it is there |
| ------- | --------------- |
| `web` | plain-text logs, plus a published port so `PortsSummary` has duplicates to collapse |
| `api` | JSONL logs with a nested `http` object, for structured rendering, field filters, and stats |
| `db` | healthcheck that passes → `healthy` |
| `cache` | healthcheck that fails → `unhealthy` |
| `migrate` | exits immediately → `exited`, only visible because `ps` passes `--all` |
| `flaky` | fails until its `on-failure:3` policy gives up, so `RestartCount` settles at 3 — the only way to get a non-zero one, since a manual restart never moves it |

## Notes

- Both the authorized identity and the server's host key are generated into
  `.keys/` (gitignored) on first use and reused afterwards; no key is ever
  checked in. The e2e tests still go through trust-on-first-use every time,
  because each one connects with its own empty `known_hosts`.
- Nesting is deep — Colima VM → dockerd → privileged dind → inner dockerd →
  demo containers. On Linux there is one layer less.
- The daemon inside is reached only over its local unix socket
  (`DOCKER_TLS_CERTDIR=""`), never exposed on the network.

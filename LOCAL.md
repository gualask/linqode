# Plan — the local machine as a target

_Last updated: 2026-09-06. Status: designed, not implemented._

Linqode connects to a host over SSH. This plan makes **the machine Linqode
runs on** a target like any other, so the same screen can watch a laptop or a
workstation with no server in the middle.

The motivation is **machine monitoring**, not Compose. A local Compose cockpit
is worth little — `docker compose ps` is already there. What is worth having is
the dashboard: meters, trends, filesystems, process table, temperatures, GPU,
on the machine under your hands.

Nothing here changes the agentless policy. That policy exists to avoid
installing anything **on a server**; locally there is no server and nothing to
install, so reading the machine directly is outside its scope rather than
against it.

## The seam

`internal/operations` depends on two methods (`observer.go`):

```go
type Executor interface {
	Exec(context.Context, string) (remote.ExecOutput, error)
	ExecStream(context.Context, string) (<-chan remote.ExecEvent, error)
}
```

Everything above that line — sampler, probe, log engine, Compose parsers,
actions — is transport-blind. Local execution is a **second Executor**, not a
second application.

The machine readings are the one thing that does not ride on it: see
"Two readers, split by what is there" below.

## Decisions

- **`local` is a target value, not a mode.** The config's `host` field already
  accepts an SSH alias or an inline `[user@]host[:port]`; `local` joins them.
  `linqode local` works with no config, and a named entry gets a project:

  ```toml
  [hosts.laptop]
  host = "local"
  compose_dir = "~/Dev/myapp"
  ```

  No reservation logic is needed: `config.Select` already falls through to the
  inline branch for an unconfigured name, producing `Selection{Spec: "local"}`,
  which the composition root recognises. A user who defines `[hosts.local]`
  wins over the keyword, and the header says who they reached.

- **Not `localhost`.** `localhost` is a real name with a different meaning —
  `ssh localhost` goes through sshd, possibly as another user, with another
  environment. Overloading it would make that operation inexpressible. `local`
  steals nothing from any standard.

- **The machine interface never gets `local`.** The machine boundary's value is
  the gap between what an agent can do without Linqode (nothing on that server)
  and what Linqode grants (typed operations). Locally that gap is zero: the
  agent already has a shell. `Catalog.SelectHost` rejects a `local` spec with a
  typed failure, and `linqode hosts` does not list it — that command is the
  machine surface's discovery, and listing an unusable name is a lie. The
  consequence, which the README must state: `config.toml` then holds two
  categories of host, those an agent may reach and those only the operator may.

- **One config file.** `~/.config/linqode/config.toml` with N `[hosts.*]`
  tables is already the multi-environment model. A directory of fragments would
  turn "add an authorised host" into "drop a file in", which weakens the
  boundary that says machine commands read one operator-controlled location.
  `--config` stays the TUI-only override; **no auto-discovery** walking up from
  the cwd, which would add a second, invisible decider of which config a
  command obeys.

- **Scripts and `!` stay.** They are the cheapest part: both are a string
  passed to `ExecStream`, and `interactive.go` already wires them
  unconditionally, before the probe branches. Excluding them locally would mean
  writing code to remove features. There is no safety argument either — `!`
  grants nothing you did not have in the shell that launched Linqode, which is
  the opposite of its remote meaning.

- **Windows is out of scope**, not a nice-to-have. `sh -c` and the POSIX
  quoting in `compose/command.go` carry every command the tool builds, the
  probe batch is a shell script, and configured scripts are shell. It is a
  port, not a variant.

- **One environment per window.** Multi-environment is answered by running two
  instances in two shells, and that is a design position to state rather than a
  limitation to apologise for: two windows show two environments at once, the
  terminal multiplexer manages them better than a TUI could, the processes are
  isolated, and each window's header always means the same host. In-session
  switching was weighed and declined — see "Declined" below.

- **The picker is the multi-host error becoming selectable.** It is not a
  launcher. It replaces exactly one thing: `no host given and multiple hosts
  configured; pick one of: …`. It does not appear when a host is given, when
  exactly one is configured (still inferred), or when none is (the current
  message is more useful than an empty list).

  It runs **inline**, not in the alt screen, so there is one alt-screen
  transition per session exactly as today and no flash; its final frame is one
  line naming the choice, which stays in the scrollback. It is a function
  returning a name, called in the same process before the existing
  Resolve → Connect → Probe → Run path — no second process, no supervision.

  `q` and `Esc` in the picker exit to the shell with status 0 (choosing nothing
  is a valid outcome); `Ctrl-C` gives 130, the documented interruption code.
  `q` in the TUI returns to the shell, always: a `q` that lands in another
  screen is a trap, and re-running is `↑` plus Enter.

  When stdin or stdout is not a terminal the picker is not drawn and the
  current error message is printed instead. `cmd/linqode/prompt.go` already
  does this for `AskPassphrase` via `term.IsTerminal`.

- **Execution semantics.** Configured scripts and `!` run under `sh -c` in the
  home directory, matching the remote rule ("scripts and `!` run in the login
  directory, like `ssh host 'command'`"). `$SHELL -c` would be closer parity
  with sshd's login shell, but "scripts run under `sh`" is a rule read once,
  while "it depends on your login shell" is behaviour discovered after a debug
  session.

- **The environment is inherited, and declared.** A local command inherits the
  operator's environment, which is right — without their `PATH` docker would
  not be found. But it carries a trap: with `DOCKER_HOST=ssh://prod` exported,
  a local session shows `local` in the header and talks to production. The
  probe must therefore read `DOCKER_HOST` and `DOCKER_CONTEXT` and the screen
  must show them when set. Readings must also run under `LC_ALL=C`: on an
  Italian locale this Mac prints `{ 1,41 1,31 1,26 }` for the load average, a
  decimal comma the remote path never sees because sshd's environment is
  minimal.

## Two readers, split by what is there

**`/proc` where there is `/proc`; native where there is not.**

- **Remote** keeps the marker-sectioned batch over SSH. Unchanged.
- **Local on Linux** keeps the same batch, run through the local Executor. Zero
  new code, numbers identical to the remote path, PSI included.
- **Local on macOS** uses a native reader behind the same `host.Metrics` type,
  selected by build tag. The remote path never imports it.

What was rejected: a **macOS variant of the commands**. It would be a second
`internal/host` — `sysctl`, `vm_stat` (pages, not KB), `netstat -ib`,
`kern.boottime` — with different commands *and* different parsers, never
exercised by the e2e suite (Alpine), and it would still not deliver the CPU
percentage. See the next section.

There is one thing the split must not lose. A machine without `/proc` still
answers `df -Pk`, and disk pressure is worth watching locally. Measured: on
input where every marker is echoed but every `/proc` read fails, `host.Parse`
returns **no error** and a `Metrics` with `load=0 cpus=0 memTotal=0` beside a
**real** disk reading — a header that is half true, half fabricated, and silent
about it. So:

- the **probe** gates whole capabilities that become nil fetches (process
  table, container cgroup columns), which is what its package comment says it
  is for;
- the **parser** reports absent sections as absent rather than as zeros,
  extending the `Pressure.Present` pattern already in `host.Metrics`.

## What macOS gives, measured

Verified on an Apple M4, 10 cores, 16 GiB, macOS 25.6, with
`github.com/shirou/gopsutil/v4@v4.26.8` built at **`CGO_ENABLED=0`** — it
reaches the Mach APIs through `purego`, so the single static binary and
cross-compilation survive.

| Tier | Cost |
| --- | --- |
| Band: load, memory, swap, **cumulative CPU ticks**, network, root disk | **6.2 ms** |
| All filesystems | 0.1 ms |
| Process table (667 processes) | 12.4 ms |
| Temperatures (41 sensors) | 54.8 ms |
| GPU via `ioreg` | **24 ms** |

For scale, the `/proc` batch costs ~2 ms on the server plus the round trip, and
the connect probe 40 ms. Per policy, no reading joins the always-on tier
without a measurement; these are that measurement for the local Darwin path,
and they are this machine's, not a guarantee.

**The CPU percentage was the crux, and it resolves.** macOS has no
`kern.cp_time` and no CLI giving cumulative CPU counters: `iostat -c 2` blocks
1.01 s, `top -l 2 -s 0 -n 0` 0.67 s, and both return percentages rather than
counters. Read natively it is `user=66663.6 system=28810.6 idle=1987419.0` —
cumulative, exactly the shape the existing client-side delta arithmetic already
consumes. No new arithmetic.

**Temperatures work**, with real values (`PMU tdie12 32.8 °C`, `gas gauge
battery 31.8 °C`). This is the reading the project has never been able to
validate anywhere, because the e2e fixture has no sensor. A Mac validates it
for the first time — with the caveat below.

## GPU on Apple Silicon

Not blocked. `ioreg -r -d 1 -w 0 -c IOAccelerator` needs **no root** and costs
**24 ms** — cheaper than `nvidia-smi`, whose driver-context startup is the
reason GPUs are on the on-demand tier at all. It yields:

```
"model" = "Apple M4"
"gpu-core-count" = 10
"PerformanceStatistics" = {"Device Utilization %"=17, "Renderer Utilization %"=17,
  "Tiler Utilization %"=17, "In use system memory"=374931456,
  "Alloc system memory"=1287782400, ...}
```

Mapping onto `host.GPU`:

| Field | Source | Note |
| --- | --- | --- |
| `Name` | `model` + `gpu-core-count` | `Apple M4 (10 cores)` |
| `BusyPercent` / `BusyReported` | `Device Utilization %` | direct |
| `MemUsedKB` | `In use system memory` | bytes → KB |
| `MemTotalKB` | — | **open**, see below |
| `TempMilliC` | — | **open**, see below |
| `PowerWatts` | — | needs `powermetrics`, which needs root; unreported |

`ioreg` output is a plist-ish text format, so this is a parser in the same
family as the ones already in `internal/host`, not a new dependency.

## Open questions

1. **Unified memory has no VRAM total.** `MemTotalKB` could be left unreported
   (and `MemUsedPercent` then means nothing) or set to `hw.memsize` (honest
   about the architecture, but changes what the percentage means across
   vendors). Decide before writing the view.
2. **No labelled SMC sensors.** Apple Silicon exposes `PMU tdieN` /
   `PMU2 tdieN` — real die temperatures with no semantic name, so nothing can
   say "GPU 45 °C". Options: show the maximum, show a compact list, or map
   known keys per chip generation (fragile). Affects both the system view's
   temperature section and `GPU.TempMilliC`.
3. **The gopsutil dependency.** The stack policy prefers the listed libraries
   "unless a real blocker shows up"; a platform whose CPU counters no CLI
   exposes is one. Weigh its surface against hand-written Mach calls before
   committing.

## Not available on macOS, and acceptable

- **PSI** — a Linux kernel concept, absent by construction.
- **Container CPU/MEM/NET/IO columns** — cgroups live inside the Linux VM that
  Colima or Docker Desktop runs, unreachable from the host filesystem.
  Reintroducing `docker stats` as a fallback is declined: it is the two-second
  reading the dashboard worked to remove, and the cost is the daemon's, so it
  is the same locally.
- **GPU power draw** — root only.

## Declined

**In-session environment switching.** It gives less than two windows (one
environment at a time, where comparison wants both), moves window management
inside the TUI where the terminal does it better, loses process isolation, and
breaks the property that a window's header always means the same host — the
same class of harm the keymap policy avoids by forbidding case-variant pairs.
It also has no answer for switching out of a follow view with live filters, and
it costs a key in a deliberately small keymap.

If its machinery is ever built, the justification should be **reconnection
after a dropped link**, not switching. Today a dropped session shows a
permanent red refresh error over stale data (`status/model.go`) and the only
cure is to quit and relaunch. Reconnection needs the same plumbing — a
connecting state, prompts as dialogs, failure as a screen — and is strictly
smaller, because the same host usually re-probes to the same screen shape,
while switching changes the shape of the backend itself.

## Implementation order

1. **`internal/local`** — `Exec`, `ExecStream`, and cancellation through a
   process group (`Setpgid`, SIGTERM to the group, SIGKILL after a grace
   period). Without it an abandoned `compose logs -f` outlives the session.
   This is the whole transport half.
2. **Composition root** — `interactive.go` branches on a `local` spec, skipping
   `remote.Connect` and the "Connecting to …" line. `Catalog` rejects the spec;
   `hosts` omits it.
3. **Probe** — two marker lines: whether `/proc` is there, and
   `DOCKER_HOST` / `DOCKER_CONTEXT`.
4. **Parser presence flags** — absent sections reported absent, not zero.
5. **Darwin reader** — `host.Metrics` behind a build tag, then the GPU via
   `ioreg`, then temperatures.
6. **Header** — the word `local` plus the machine's name, and the Docker
   endpoint when it is not the local socket.
7. **The picker** — a separate phase, shippable independently.

A bonus to collect along the way: with a local Executor, part of the e2e suite
can run inside the dind container without sshd, removing scaffolding rather
than adding it.

## Reproducing the measurements

```bash
# No cumulative CPU counters from any CLI on macOS
sysctl -n kern.cp_time            # unknown oid
time (iostat -c 2 | tail -2)      # 1.01 s
time (top -l 2 -s 0 -n 0 | grep "CPU usage" | tail -1)   # 0.67 s

# GPU, no root
time (ioreg -r -d 1 -w 0 -c IOAccelerator >/dev/null)    # 24 ms
ioreg -r -d 1 -w 0 -c IOAccelerator | grep -E '"(model|gpu-core-count)"|PerformanceStatistics'

# Locale trap
sysctl -n vm.loadavg              # { 1,41 1,31 1,26 } on an Italian locale
LC_ALL=C sysctl -n vm.loadavg     # { 1.47 1.32 1.27 }
```

The native readings were timed with a throwaway module importing gopsutil v4 at
`CGO_ENABLED=0`; re-create it to re-measure on other hardware.

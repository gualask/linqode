# Roadmap scratch — dashboard UX, sampling budget, system metrics

_Draft, September 2026. Not part of `docs/`: a parking place for work that is
designed but not scheduled. Fold the surviving parts into `docs/PROJECT.md`
(roadmap + decided policies) and `docs/architecture.md` when it is picked up,
then delete this file._

Two threads that turned out to be one: a UX refactor into focusable panels,
and the sampling cost that dictates what those panels may contain. The GPU and
temperature feature that started this file is now a late phase of the same
sequence, because a batched sampler makes it nearly free.

## 1. Target

A **home dashboard** that gives an overview at a glance, where each region can
take focus and be opened one level deeper. The service table stays the anchor
at full width; the host band is a permanent one-line header; satellites
degrade first.

**The home shows vital signs, the detail views are the investigation
surface.** Every reading a later phase adds lands in a detail view, not in a
new home panel — which is what keeps the home cheap to draw and cheap to
sample.

```
┌ linqode ─ user@host ─ /srv/app ── 4 running · 1 exited ─────────────────┐
 host  cpu[▇▇▃░░ 34%]  mem[▇▇▇▇░ 3.1G/7.8G]  /var[▇▇░░ 24G/98G]  up 12d
┌─ services ─────────────────────────────────────────────────────────────┐
│ NAME      STATE    HEALTH   RESTARTS  CPU    MEM    NET      IO        │
│ api       running  healthy  0         2.1%   410M   1.2M/3M  0B/12M    │
└────────────────────────────────────────────────────────────────────────┘
┌─ events ───────────────────────────────────────────────────────────────┐
│ 12:04  worker  health_status: healthy     12:03  worker  restart (137) │
└────────────────────────────────────────────────────────────────────────┘
 tab panel · enter open · c actions · x scripts · ! run · r refresh · q
```

`enter` on the band opens the system view, which is where phases C, E, F and
G put their readings:

```
 host  cpu[▇▇▃░░ 34%]  mem[▇▇▇▇░ 3.1G/7.8G]  …            (band stays)
┌─ system ───────────────────────────────────────────────────────────────┐
│ cpu    ▇▃▁▁▂▁▁▁   load 0.42 0.51 0.60        temp  58 C      (F)       │
│ mem    3.1G/7.8G  swap 0/2.0G   pressure 2.1%                (C)       │
│ disk   /  24G/98G      /var/lib/docker  61G/98G              (C)       │
│ gpu    NVIDIA A10  34%  4.1G/24G  61 C  120W                 (G)       │
│ top    1.1G postgres · 410M node · 96M redis                 (E)       │
└────────────────────────────────────────────────────────────────────────┘
 esc back · j/k select · …
```

Responsive rule: below ~100 columns the satellites stack or drop to one;
below ~26 rows they disappear. The band is one row and always survives, so
the degraded screen is what ships today.

## 2. Decisions taken (in discussion, September 2026)

- **A border must earn its cells.** Two columns and two rows per box is the
  same cost that got the sidebar removed (`internal/tui/status/hostband.go`
  header comment). Only a region that can take focus draws a border; a purely
  informative region stays a bare band.
- **Anchor layout, not a symmetric grid.** A 2x2 grid at 80x24 leaves each box
  ~36x7 of content, which is narrower than the service table needs: the grid
  would degrade the primary object to make room for secondary ones. btop can
  afford a grid because it reads a local `/proc` for free and all its panels
  are graphs; we cannot.
- **The host band is a permanent header, and it is focusable.** Always drawn,
  one row, on every screen of the home including the detail views. It takes
  focus as the first stop in the ring; focus shows in its `host` label and in
  the footer hints, not in a border, so it stays one row. This retires the
  idea of a host *satellite* panel: it would have shown the band's own data a
  second time.
- **`enter` descends one level on whatever has focus.** Band -> the system
  view; services table -> the selected service (its logs, exactly as today);
  events -> the container behind the event. `esc` comes back up. One concept,
  and no existing binding changes meaning.
- **Maximising a panel is a separate gesture, deferred.** Opening a detail
  changes context; making a box full-screen changes layout only. With one box
  on the home it buys nothing, so it stays unbound until the home is crowded
  (zenith's `e`/`m`, or btop's `1`-`9`, when that day comes).
- **Events, not raw logs, on the home.** Raw logs on a dashboard are noise
  that scrolls, and they are the one panel that costs continuous bandwidth.
  Per-service logs are already one `enter` away, which is the right place.
- **History is client-side.** Samples already fetched, kept in a ring buffer,
  give sparklines and graphs for zero extra remote cost.

## 3. Open decisions (gates, not details)

1. **`docker events` as the refresh trigger** (phase D2). Replacing the 5s
   `compose ps` timer with event-driven invalidation changes the update
   contract, so it belongs in PROJECT.md's decided policies, not in a commit
   message. Recommendation: do it — but as its own gated step.
2. **Process actions (`kill`) in the system view** (phase H). Today's
   mutation surface is deliberately narrow: compose lifecycle, configured
   scripts, and `!` as the only arbitrary human capability
   (`docs/architecture.md`, "capability-based"). `kill` widens it to
   "operations on the system". Decide before writing it.
3. **Engine API instead of the Compose CLI** (section 5). Reaching the daemon
   through `docker system dial-stdio` would remove the CLI startup from every
   `ps` and give native event and stats streams, but the principle written
   today is "drive standard CLIs remotely and interpret their output locally"
   (`docs/PROJECT.md`, Vision). Switching is a policy change, and it is not
   all-or-nothing: reads could move while mutations stay on `docker compose`.

## 4. Performance model

The budget table in `docs/PROJECT.md` was measured **over loopback** against
the fixture: `true` at 1 ms means the remote fork is free, not that an exec is
free. On a real host every exec costs at least one RTT. Two mitigations
already in place: one SSH session with a *channel* per exec (no handshake),
and `tea.Batch` in `internal/tui/status/model.go:117`, so execs run
concurrently and latency does not accumulate. What does accumulate is remote
shell forks, jitter on a lossy link, and the window where one round of samples
overlaps the next.

So the metric to minimise is **execs per tick** and **ticks per minute**:

1. **One exec for everything cheap.** Extend the marker protocol of
   `host.Command()` rather than adding sibling commands: `/proc/stat`,
   `/proc/meminfo`, `/proc/net/dev`, `/proc/pressure`, `df -Pk` with no
   argument, `ps`, thermal sensors. One command, one round trip, a few KB.
   Adding a reading to that batch costs zero extra execs — which is why the
   list in phase C is longer than it looks like it should be.
2. **Do not poll what a stream can announce.** One `docker events` stream
   (~0 traffic when idle) re-runs `ps` only when something changed, plus a
   slow safety-net refresh. On an idle deployment this drops the steady-state
   cost to nearly nothing *and* makes the table more reactive than a 5s timer.
3. **Three cadences.** Static, read once at connect (core count, MemTotal,
   kernel, docker version, filesystem sizes, GPU presence). Slow, 30-60s (disk
   usage). Normal, 2-5s (load, cpu, mem, net). Expensive, on demand or 20s
   (`docker stats`, which cannot be made quick).
4. **Only visible panels sample.** Each panel declares what it needs; the
   scheduler unions the needs of the panels actually drawn. A collapsed panel
   costs nothing, and a zoomed one may sample *faster* — detail is paid for
   only where the operator is looking.
5. **Never queue a sample.** If the previous one is still in flight when the
   tick fires, skip the round; measure the round-trip and stretch the interval
   on slow links (witr's "adaptive cadence").
6. **History is free.** See section 2.

## 5. Why agentless, and what falls out of it

**Product framing** _(settled in discussion, September 2026)_: Linqode
**monitors when the operator wants to monitor, and helps them investigate the
host**. It is not a monitoring system. Reconsidered explicitly against the
alternatives below, and kept.

| | Where it samples | What crosses the wire | What must be installed |
| --- | --- | --- | --- |
| 1. Tool on the server (`htop`, lazydocker over ssh) | on the host, free | a **rendered screen** | the tool, on every host |
| 2. Hybrid: agent + client (Netdata, node_exporter, Beszel, Glances server mode) | on the host, free | a compact structured payload | a daemon, supervised, with a port |
| 3. Agentless over SSH (Linqode today) | on the client, after asking | command output | nothing |
| 4. Remote API tunnelled over SSH (`DOCKER_HOST=ssh://`) | on the host for Docker data | structured JSON, streaming | nothing |

Variant 1 has a failure mode that is easy to miss: the wire carries an
*interface*, not data. A colour repaint of 80x24 is 10-20 KB, and every
keystroke costs a round trip before the cursor moves. At 200 ms RTT, `htop` on
the server is worse than a local dashboard sampling every three seconds — and
it would take the local log engine, the JSON machine interface, and hosts you
cannot install on with it.

**What agentless actually costs.** After phase B, one sample is a shell fork,
a handful of `cat`s, one RTT, and a few KB, every 2-5 s: ~1-2 KB/s and no
measurable load on the host. An agent would save the fork and the round trip.
Real, but not the bottleneck.

**The bottleneck is the CLIs we drive, not SSH.** `docker stats` costs ~2 s
because the *daemon* reads each container's cgroups twice, a second apart. An
agent would be fast there not because it is local, but because it would read
the cgroup files directly — which we can do too. Three options follow, none of
which needs anything installed:

- **Container CPU and memory from cgroups directly**:
  `/sys/fs/cgroup/.../cpu.stat` and `memory.current` per container, deltas
  computed client-side like `/proc/stat`. No 2-second wait, inside the batched
  exec. Paths derive from container ids (`system.slice/docker-<id>.scope` with
  the systemd driver, `docker/<id>` with cgroupfs). Would retire the soft
  `docker stats` poll entirely.
- **`docker system dial-stdio`** — the mechanism behind `DOCKER_HOST=ssh://`:
  the daemon speaks its API over stdio on one SSH channel. No compose CLI
  startup per `ps`, native `/events` and `/containers/{id}/stats?stream=1`,
  structured JSON instead of parsed CLI output. Gated by decision 3.3; verify
  the minimum daemon version and whether Compose project grouping is
  reproducible from `com.docker.compose.*` labels.
- **A persistent remote sampler**: one long-running streamed command, in
  substance `while :; do <marker batch>; sleep 3; done`, emitting a sample
  every N seconds. It buys an agent's per-sample cost — zero round trips,
  latency irrelevant, which changes everything on a 300 ms link — while
  installing nothing: it is an "agent" only in that it lives as long as the
  session and dies with the channel. Costs: the cadence is fixed until it is
  restarted, errors are harder to attribute, and termination must be exact.
  The pipeline already exists (logs, `docker stats --stream`).

**What an agent would actually buy**, once performance is off the table:
history while nobody is connected, alerting, and fleet. All three are already
non-goals in `docs/PROJECT.md`, and the framing above settles them: this tool
is opened to answer a question. Reopen the question the day retention or fleet
is wanted — it will be a product decision, not a performance one.

**What the framing implies for this plan.** It is the same rule the cost
budget already encodes ("a server pays a small fixed rent for what is on
screen, and pays by the second only while someone is watching"), extended:
the home is what you see the moment you connect, the zoom is what you ask for
while investigating, and a panel earns its place by shortening an
investigation — not by being a number somebody might want.

## 6. Sequence

Ordered by dependency, each phase leaving `go test ./...` green and a frame
rendered and looked at (see section 8).

### A. Panel model: chrome, focus, detail views

No new data, no new remote cost — deliberately, so the idea can be *looked at*
before anything is built on it. If SSH traffic changed at the end of this
phase, something went wrong. Three commits, each green and each worth looking
at on its own.

**A1 — `internal/tui/panel` and theme tokens** _(done, September 2026)_. A
pure addition, wired to nothing, fully unit-testable.

- `panel.Box`: border, title in the top rule, title truncation, and the size
  arithmetic (content gets `w-2` / `h-2`, so no caller has to know that).
- A `Panel` interface: title, view at a size with a focused flag, footer
  hints, update, and a data-needs declaration that nothing consumes yet —
  phase B is its first consumer.
- Focus tokens in `theme`: focused border colour against a grey one, focused
  title bold and coloured against dim. **Never reverse video for focus** —
  reverse is already the selected row, and two things that both mean "here"
  cancel out.
- The corollary, which is easy to miss: the selected row of an *unfocused*
  panel must not stay a lit bar. `table.go` renders it with `theme.Reverse`
  today; it needs a quieter form for when its panel does not hold focus.
- The border vocabulary already exists: `renderMenu` in
  `internal/tui/status/commands.go:129` draws a bordered box for the action
  menu.

**A2 — `internal/tui/home` takes over the screen** _(done, September 2026)_.
The risky commit: not drawing borders, but taking the screen role away from
`status`.

- The new package owns size, the panel set, focus, the title line, the
  footer, the modal menu (`c`, `x`) and the `!` prompt.
- `status` shrinks to the **services panel**: table, columns, stats,
  fetches, selection. It loses `View()`, footer, title, menu and prompt.
- `tui.go` routes `home <-> follow` instead of `status <-> follow`.
- The footer composes: global hints plus the focused panel's. The
  priority-dropping logic in `internal/tui/status/view.go:117` moves as is;
  only its input changes.
- Visually this changes one thing: the table gains a border and a title.
  Looked at in color: the border **keeps** its four cells. Blue against grey
  reads immediately as which region the keys are pointing at, the hairline
  never competes with the filled heading band below it, and the quiet fill on
  an unfocused panel's cursor row does the rest.

**A3 — the band joins the ring, and the system view appears** _(done,
September 2026)_.

- `hostband.go` moves out of `status` into `internal/tui/system`, which owns
  two renderings: the one-row band and the full system view.
- Focus ring: band, then services. `tab` / `shift-tab` move; `enter`
  descends; `esc` returns. Focus on the band shows in its label, not in a
  border.
- The system view is born nearly empty — load 1/5/15, cores, memory, disk,
  uptime, of which the band shows four numbers today. It is the shell phases
  C, E, F and G fill in.
- Fell out of the work, and worth keeping: **the host sampling moved to the
  screen**. Neither panel can own it — the band and the system view are the
  same sample — so `home` grew the fetch, the interval and the no-overlap
  guard. That is the shape phase B's scheduler wants, one tier early.
- Fell out of *looking* at it: **`r` became a screen command**. As a panel key
  it silently did nothing whenever focus was on the band, and what an operator
  means by refresh is "read everything again, now".

### B. Sampling scheduler

Invisible on screen; it is what makes everything after it affordable.

- One batched exec for all cheap readings, extending the existing marker
  sections and best-effort parsing (a missing section leaves its fields zero).
- The three cadence tiers, including a **static probe at connect**.
- No-overlap rule and adaptive backoff on measured round-trip.
- Visibility-driven sampling, consuming phase A's declarations.
- **Container CPU and memory from cgroups** instead of `docker stats` (see
  section 5): the same batched exec, deltas client-side, no 2-second wait.
  Retires the soft poll and is the single largest cost removal in the plan.
- Re-measure with `go test -tags e2e ./tests/e2e/ -run TestRemoteCommandCost
  -cost.measure`, and record the loopback caveat in the PROJECT.md table so
  the next reader is not misled the way this one was.

Two options recorded in section 5 that this phase may adopt but does not
require: the **persistent remote sampler** (worth it on high-latency links,
and cheap to try once the batch exists) and the **Engine API over
`dial-stdio`** (gated by decision 3.3 — decide before building on it, since it
also changes how phase D gets its events).

### C. Richer host readings

Free on top of B: same exec, more markers. The largest visible gain per
millisecond in the whole plan.

- **Per-core CPU** from `/proc/stat` deltas (computed client-side across
  samples). Load average alone hides the common case: one core pinned, seven
  idle.
- **Swap** alongside memory — filling swap precedes trouble that
  `MemAvailable` does not show.
- **All filesystems**, not just `/`. Literally free: the same `df` without its
  argument. A full `/var/lib/docker` is one of the most common causes of a
  dead deployment, and today the screen cannot show it.
- **Network throughput** from `/proc/net/dev` deltas.
- **Pressure** from `/proc/pressure/{cpu,io,memory}` when present — the best
  single "is this machine suffering" signal, and better than load average.
  Absent on kernels without PSI (on several Debian/Ubuntu builds it needs
  `psi=1` at boot), so it follows the existing rule: no reading, no meter.
- **Client-side history ring buffer** feeding sparklines.

### D. Events

- **D1 — events panel.** One persistent `docker events --format json` stream
  through the existing follow pipeline, rendered as the "what just happened"
  feed: restarts, health transitions, OOM kills. Additive and low risk.
- **D2 — event-driven refresh** _(gated on decision 3.1)_. `ps` re-runs on
  change instead of on a timer, with a slow safety-net refresh.

### E. Detail views with on-demand data

The payoff of A + B: data too expensive for the home, fetched only when a
panel is opened.

- System view → **top processes** by RSS/CPU. Not a home panel: on a Docker
  host most of the top processes *are* the containers already in the table.
  Compatibility: `ps --sort` is procps and absent on busybox, and `-eo` there
  depends on the build — verify against the fixture and fall back to sorting
  client-side or parsing `ps aux` positionally. Same class of variability that
  made `internal/host` read `/proc` instead of `free`.
- Filesystem panel → **`docker system df`** (images, volumes, build cache).
  Slow, because the daemon walks images and volumes; a real question
  ("is Docker filling my disk?") that deserves an on-demand answer.

### F. Temperatures

A marker section in phase B's batch, a reading in the band, a row in the
system view.

- **`/sys/class/hwmon/hwmon*/`** is the real source, the one `sensors` merely
  formats: `name` gives the chip (`coretemp`, `k10temp`, `nvme`,
  `cpu_thermal`), `temp*_input` the value in milli-degrees C, `temp*_label`
  the label (`Package id 0`, `Tctl`), `temp*_crit` / `temp*_max` the
  threshold.
- **`/sys/class/thermal/thermal_zone*/{type,temp}`** is the poorer fallback:
  meaningful on many ARM SoCs (on a Raspberry Pi `thermal_zone0` is the CPU),
  often only `acpitz` on x86.
- One command keeps the file→value association:

  ```sh
  grep -H '' /sys/class/hwmon/hwmon*/name /sys/class/hwmon/hwmon*/temp*_input \
             /sys/class/hwmon/hwmon*/temp*_label /sys/class/hwmon/hwmon*/temp*_crit 2>/dev/null
  ```

- `hwmon` is preferred because `temp*_crit` supplies a denominator: a
  temperature is not a percentage of anything, but `input/crit` is — which
  yields a meter consistent with the others instead of a bare number.
- Open: which sensor to show when several report (hottest with its label, or a
  preference list `coretemp` / `k10temp` / `cpu_thermal` with max as
  fallback); what to do when `crit` is missing (fixed 100 C scale, or a number
  with no bar).

### G. GPU

Detection belongs to phase B's static tier; sampling gets its own cadence.

- **NVIDIA**: `nvidia-smi --query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw --format=csv,noheader,nounits`.
  Present wherever the driver is, already tabular, one row per GPU. **Not
  free**: driver context initialisation typically costs hundreds of ms (worse
  with persistence mode off and the GPU in a low-power state), so it never
  shares the sysfs tick. Measure it with the cost harness.
- **AMD**: `/sys/class/drm/card*/device/` exposes `gpu_busy_percent`,
  `mem_info_vram_used`, `mem_info_vram_total`, with temperature and power in
  its own `hwmon` — i.e. sysfs cost, so it *can* ride the batch. `rocm-smi`
  requires ROCm installed.
- **Intel**: little without installing anything. `intel_gpu_top -J` needs the
  package and privileges; sysfs gives frequencies, plus an i915 hwmon on dGPUs
  and recent kernels.
- **Probe once at connect** (`command -v nvidia-smi`, or the `vendor` under
  `/sys/class/drm/card*/device/`) so a GPU-less host never pays `nvidia-smi`
  startup.
- Open: one meter per card or an aggregate; whether the GPU gets its own panel
  or one row in the system view.
- Out of scope here: **per-container** GPU attribution, which would mean
  crossing `nvidia-smi --query-compute-apps=pid,used_memory` with container
  PIDs. `docker stats` reports nothing about GPUs.

### H. Context actions in detail views _(gated on decision 3.2)_

Signals to a process from the system view, and whatever the other detail
views justify. Last, because it is a policy change wearing the
costume of a feature.

## 7. Deliberately not on the home

- **Raw logs** — noise on a dashboard, continuous bandwidth, and already one
  `enter` away per service.
- **Listening ports** — mapping a port to its process needs root, and the
  published ports are already in `compose ps`. Possible later inside the host
  panel's detail (witr's idea).
- **`docker system df` and image/volume inventories** — phase E, on demand.

## 8. Per-phase obligations

- **Look at it in colour.** `docs/tests.md`, "Looking at the UI": a change to
  how the interface looks is not finished until a frame has been rendered and
  looked at, because the suite runs without a TTY and sees no colour at all.
  Every phase adds its states to `internal/tui/status/uishot_test.go` (which
  may need to move or be duplicated once panels live outside `status`, per the
  note there about copying before promoting).

  ```bash
  LINQODE_UI_SHOT=/tmp/shot.html go test ./internal/tui/... -run TestUIShot
  ```

- **Cost.** Any phase adding a remote command re-runs the cost harness and
  updates the PROJECT.md table.
- **Docs.** `docs/architecture.md` (the "Compose status" flow describes the
  current band-and-table layout in detail, and the `internal/host` row still
  says "one command over `/proc` and `df -Pk`"), `docs/PROJECT.md` (roadmap,
  cost budget, and the decided policies touched by 3.1 and 3.2), `README.md`
  for any new configuration key.
- **Fixture limits.** Alpine in docker-in-docker has **no GPU**, and the
  thermal zones it sees are the host's (absent under Colima on macOS). So
  phases F and G are unit-tested over captured output — the style
  `internal/host/host_test.go` already uses — plus an e2e test asserting
  graceful behaviour when the sensors are *absent*. Real hardware is validated
  in the "hardening against real deployments" phase already queued.

## 9. Prior art consulted for this plan

Complements the table in `docs/PROJECT.md`.

| Tool | What to take |
| ---- | ------------ |
| [docksurf](https://github.com/praneeth-etta/docksurf) | Reacts to Docker events instead of polling — the origin of rule 2 and phase D. Also selection-preserving updates, which we already do. |
| [zenith](https://github.com/bvaisvil/zenith) | `Tab` between sections, `e`/`m` to expand/minimise the active one: zoom without a second view. Scrollable chart history — free for us, see rule 6. `Enter` on a process for detail. |
| [ku](https://github.com/bjarneo/ku) | Cockpit overview; a persistent status bar showing the *active* keybindings; `?` for help. It previews the equivalent `kubectl` command, which is the same choice we already made for `docker compose` actions — keep it. |
| [btop](https://github.com/aristocratos/btop) | The visual vocabulary of boxes per subsystem, and the warning attached to it: btop reads a local `/proc` for free and can afford four full panels. We cannot — hence three panels, not six. |
| [witr](https://github.com/pranshuparmar/witr) | Out of scope for the home, but "listening port → owning process" is a good candidate for the system view, and its adaptive refresh cadence is rule 5. |

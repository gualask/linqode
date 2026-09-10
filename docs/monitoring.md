# Monitoring

_Last updated: 2026-09-09._

What Linqode reads off a remote host, how often, and why each reading is on
the tier it is on. The screen those readings are drawn on is
[interface.md](interface.md); everything Linqode *does* to a host rather than
reads from it is [operations.md](operations.md).

The rule the whole design encodes: **a server pays a small fixed rent for
what is on screen, and pays by the second only while someone is watching.**

## One owner of the cadence

Everything goes through a sampler in `internal/tui/home`: one heartbeat a
second asks each source whether it is due. Panels render what they are
handed; none of them owns a timer. `r` reads everything again on demand,
whatever the intervals say.

A source carries four things beyond its fetch:

- **Its own interval.** Measured start to start, so a slow read does not
  shorten the next wait.
- **A no-overlap rule.** A read already in flight is never queued behind
  another.
- **A gate.** A source nobody is looking at is never due at all. The
  container counters stand down while the live stream fills the same
  columns; the process table, the graphics cards and `docker system df` are
  not read while the system view is closed.
- **A stretch.** A read that occupies more than a quarter of its own interval
  widens it. On a link where `ps` takes a second, asking every five keeps a
  command in flight most of the time, and the honest response is to ask less
  often rather than to overlap.

A source that declines to start — the container counters have nothing to
address before the first service list lands — is left exactly as it was, so
the next beat can try again. Marking it in flight would strand it forever,
since no answer is coming back to say it finished.

### Three tiers

| Tier | Cadence | Readings |
| ---- | ------- | -------- |
| Event-driven | when the daemon says so, 60 s safety net | the service list |
| Always on | 5 s | the host batch, the container counters |
| On demand | only while the system view is open | the process table (3 s), graphics cards (5 s), `docker system df` (30 s) |

The on-demand tier is read the *moment* its view opens rather than at the
next beat: the source was not due a moment earlier, so waiting would be
waiting for nothing.

## The capability probe, on no tier at all

One round trip at connect, before anything else runs, establishing what
decides whether the rest of this document even applies: whether docker is
installed, whether this user may reach the daemon, which compose the host has,
whether the configured `compose_dir` exists, whether the host has the `/proc`
every reading below is made of, and which daemon `DOCKER_HOST` or
`DOCKER_CONTEXT` will send those commands to. **39 ms and 183 bytes**,
measured, once per session — against the 66 ms `compose ps` pays every minute.
The three sections added last cost 35 bytes of answer, two shell builtins and
no round trip: the time is the same 39 ms it was at 148 bytes, because what
this pays for is the daemon round trip and the compose CLI start, not the
shell.

It is on no tier because none of it is a reading. A tier is a cadence, and a
cadence assumes the answer changes; these do not change while a session is
open, and a host that gains a docker group mid-session is a reconnect, not a
refresh.

**The rule for what goes in it**: probe what changes what the interface can
offer or what error it can explain; guard inline what only changes one
command's fallback. The two inline guards below are on the other side of that
line and stay there — `timeout` for the mount list, `command -v nvidia-smi`
for the graphics cards. Both change one command's fallback and nothing else,
and as probe state they would be machinery for nothing.

**`/proc` is asked about because one capability is nothing else.** The
readings below are files under `/proc` — the load average, the memory, the CPU
counters, the process table — and a host without it answers the batch with
eight empty sections beside a real `df`. The screen already declines to draw a
meter it has no number for, so most of that needs no probing. The process
table is the exception: it is `/proc/<pid>/stat` and nothing else, so on such
a host it is not a reading that comes back empty but a panel that could only
ever open empty, and the probe turns it off the way it turns off compose.

**The docker endpoint is asked about because of the local target.** A local
session inherits the operator's environment, so a header saying `local` can be
driving production through an exported `DOCKER_HOST`, and nothing else on the
screen would give that away; the header says so when it is set (see
[interface.md](interface.md)). Over SSH the variables are almost always unset,
sshd's environment being minimal — but a host that does set one is a host
where the same sentence is worth showing, and asking costs one `echo` either
way. There is no second batch for a second transport.

**One section of the batch is not a condition at all**, and is the one
deliberate exception to that rule: the last one reads `PRETTY_NAME` out of
`/etc/os-release`. Nothing turns on it — it decides no capability, explains no
error, and a host that does not answer it loses nothing. It is here because the
round trip is already being paid and what a machine calls itself costs a `grep`
on the end of it, and it goes where that belongs: the last row of the system
view (see [interface.md](interface.md)). Named here as an exception rather than
left to be found in the batch by whoever reads it next.

The mechanism is the host batch's: marked sections, evidence rather than
verdicts, the verdict taken on the client, and a section that is missing
degrading to unknown rather than failing the result. Unknown reads as "carry
on" everywhere it is consulted, which is the rule the whole thing turns on: a
probe that established nothing must never be the reason a working host loses
its table.

Two details are load-bearing. The daemon section captures stderr, because on a
refused socket everything worth reading is there and the message names the
socket it tried. And `docker compose version` needs the CLI but not the
daemon, so a host whose socket refuses this user still reports which compose it
has — which is what makes "no docker here" and "docker is here and will not
talk to you" two different sentences instead of one empty table.

What is done with the findings is [interface.md](interface.md) for the screen
and [operations.md](operations.md) for the machine adapter.

## The service list

`docker compose ps --all --format json` in the project's directory, parsed
into typed rows — both the NDJSON and the legacy array shape are accepted.
Selection is preserved on the same container across refreshes; a failed
refresh shows the error while the last good table stays on screen.

Restart counts are not in that output, so the same refresh follows it with
`docker inspect --format '{{.Name}} {{.RestartCount}} {{.State.Pid}}'` over
the containers `ps` just named — cheaper than a second compose invocation,
which would pay the compose CLI's startup again to re-derive a list already
in hand. It is best-effort: it never fails a refresh, and inspect's non-zero
exit is ignored, since a container that disappeared between the two commands
makes it fail while the remaining lines are still good. Counts that did not
arrive render as `-`, distinct from a container that has genuinely never
restarted. The pids on the same line are what the container network counters
are addressed by.

### The daemon is watched, not polled

What triggers that read is the daemon, not a clock. One `docker events`
stream, scoped by the project label `ps` reported and filtered server-side to
the actions that change a row, re-reads the table as soon as something
happens. News arriving while a read is already in flight is remembered rather
than dropped: that read answers a question older than the news, so another
follows it.

Three things make this a policy rather than an optimisation:

- **The filtering is on the server.** Health checks emit `exec_create`,
  `exec_start` and `exec_die` for every probe of every container — measured
  at thirty of thirty-seven events in ten seconds against a single container
  probing every two seconds — so an unfiltered stream would cost more
  bandwidth on an idle project than the polling it replaces. The project
  label is what keeps another tenant's containers on a shared host out of
  this session.
- **The timer steps back rather than away.** While the stream is up the
  service list still refreshes every sixty seconds, for what no event
  describes and for a stream that stopped delivering without saying so.
  Losing the stream restores the short interval.
- **Watching is an optimisation, not a capability.** A daemon that refuses
  the stream leaves the screen on its timer and says nothing: there is
  nothing an operator could do about it.

Each event carries the daemon's own `{{.Time}}` rather than the moment the
line was read. Normally the two differ by the drain interval, but a link that
stalls and then delivers a burst is exactly when the feed is worth reading,
and client-side stamping would give every event in that burst the same wrong
time. The format is pipe-separated because docker does not interpret `\t` in
a format string — it prints the two characters — and `{{index .Actor.Attributes "name"}}`
rather than a field lookup, because a missing attribute yields the zero value
instead of the literal `<no value>`. Both were measured against a real daemon,
not assumed.

## The host batch

One command, marker-sectioned, carrying every cheap reading about the
machine: load, uptime, memory and swap, per-core CPU, every network
interface, PSI pressure, temperatures, the root filesystem and the full mount
list. Measured at **6 ms and 2.8 KB**. Adding a reading to it costs bytes and
not a round trip, which is why the list is longer than a per-command budget
would allow.

It reads `/proc` and POSIX `df -Pk` rather than parsing `uptime(1)` or
`free(1)`, whose output formats differ between distributions, busybox and
versions. Missing or malformed sections leave their fields zero instead of
failing the whole sample: a host that does not expose one of these is still
worth showing the rest of.

### Counters, not values

`/proc/stat` and `/proc/net/dev` count since boot. A single reading of either
is a large number that means nothing; the difference between two is the
percentage and the rate an operator reads. That arithmetic is done on the
client — the same reason the container counters are, and the alternative is
asking the server to sample twice and sleep in between.

The interval the difference is divided by comes from `/proc/uptime`, not from
the client's clock. It is the interval the counters were actually accumulated
over, so a slow round trip or a stalled UI cannot turn a quiet second into a
spike — and a reboot between two samples is detected rather than divided by.

### Filesystems

The root filesystem keeps its own `df -Pk /`, and the full mount list is a
second `df` guarded by `timeout 5` where `timeout` exists. That is not about
cost: `df` with no argument calls statfs on every mount, and a hung network
mount holds it for as long as the kernel allows. The one reading that is
always wanted must not wait on the list.

The list keeps provisioned storage, including a dedicated filesystem at
`/var/lib/docker`. Pseudo-filesystems and system bookkeeping mounts are
excluded; duplicate bind mounts are shown once. **`/` is kept
unconditionally**: on a containerised host it is itself an overlay, and the
filter would otherwise drop the mount that matters most.

### Temperatures

`/sys/class/hwmon` — the files `lm-sensors` merely formats, and which are
there whenever the hardware is, on hosts where `sensors` is not installed and
cannot be. `name` says what the chip is, `temp1_input` the reading in
thousandths of a degree, `temp1_label` what that sensor measures, and
`temp1_crit` or `temp1_max` the manufacturer's limit.

That limit is why hwmon is preferred to `/sys/class/thermal`, which is kept
only as the fallback for the ARM boards that expose nothing else. A
temperature is not a percentage of anything — 58 degrees is meaningless
without knowing what the chip tolerates — but `input/crit` is, which yields a
meter consistent with every other one on the screen. It also settles which
sensor to show: the one closest to **its own** limit, because the bare
numbers get the comparison backwards. An NVMe at 71 °C is nearer trouble than
a CPU at 80 °C. Where no limit is reported the reading is drawn against a flat
100 °C and the row says so, since the denominator is then ours.

Only `temp1` is read, which by hwmon convention is the chip's principal
sensor — `Package id 0` on coretemp, `Tctl` on k10temp, `Composite` on an
NVMe. The higher-numbered ones are per-core, and reading them would make this
section's cost scale with the *core* count to produce sixteen numbers nobody
acts on. As written it scales with the number of chips, which is a handful on
any machine.

On the many hosts with no sensors at all — most virtual machines, and the e2e
fixture — the globs match nothing, grep says so on stderr, and there is no
reading and no meter.

## Container counters

CPU, memory, block I/O and network per container, read from the kernel rather
than asked of the daemon: the cgroup files under `/sys/fs/cgroup`, plus
`/proc/<pid>/net/dev` for the network, whose pids arrive with the restart
counts. All of it is world-readable, so the operator account needs no
privilege it did not already have — including the network counters, which
look as though they should need one and do not: the ptrace check that guards
`/proc/<pid>/environ` does not apply to `net`.

Both cgroup v1 and v2 layouts are read, and both drivers of v2 (`docker/<id>`
with cgroupfs, `system.slice/docker-<id>.scope` with systemd). `compose ps`
reports a twelve-character id and the cgroup directory carries the full one,
so readings are matched by prefix with a twelve-character floor.

**Measured at 6 ms against `docker stats --no-stream`'s 2.03 s.** That two
seconds is fixed sampling latency, not project size: the daemon reads each
container's cgroups twice, a second apart, to derive a CPU percentage. It can
never be made quick, and it does not need to be — the second reading is the
previous sample, which the client already has. Two modes follow:

- **Sampled**, always on: the cgroup counters every 5 s, behind the table's
  CPU, MEM, NET RX/TX and IO R/W columns. The interval is now what an
  operator can use rather than what the server can bear, and five seconds
  means the first CPU percentage — which needs two readings to exist at all —
  arrives while they are still looking.
- **Live**, on request (`a`): `docker stats` in its streaming form, a sample
  per second, for as long as the panel is open. Streaming is the one form
  where docker's own sampling is not a tax, and it stays the source of the
  sparklines. The sampled source stands down while it runs — that is what its
  gate is for — and the remote command is terminated when it closes.

Both are scoped to the project's containers, since a bare `docker stats`
would report every container on the host. Docker wraps its output in
cursor-control escapes even when writing to a pipe, so the parser strips them
before reading the JSON. `docker stats --no-stream` also remains the machine
interface's one-shot: an agent asking once for a JSON reading is not holding a
dashboard open, and a single answer that needs no previous reading is worth
two seconds to it.

## The on-demand tier

Three readings are never taken for the home screen. They are what the panel
model was built for.

### The process table

`/proc/<pid>/stat` for every process, plus `getconf PAGESIZE`, `getconf
CLK_TCK` and `/proc/uptime`. Cheap in time and expensive in bytes — **3 ms
but 200 bytes per process**, so ~20 KB on an ordinary host — which is far too
much to pay every few seconds for a screen nobody is looking at.

`ps` is deliberately not the source, and measuring it against the fixture's
busybox is why: no `--sort`, **no `pcpu` column at all** (its `-o` accepts
sixteen names and none is a CPU share), `ps aux` silently ignoring its flags
and printing four columns that share neither order nor content with procps',
and `-o rss` printing `93m` rather than a number. Parsing that positionally
would have produced confident nonsense on exactly the hosts this tool is meant
to reach.

Nothing is narrowed on the server either. `sort -k24` counts space-separated
fields, and field two is a process name that may contain spaces and
parentheses — so on a host running a `Web Content`, every column after it
shifts and the sort ranks by the wrong number. The name is found on the client
by looking for its *last* closing parenthesis, and the ranking is done there
too. The page size and the clock tick are asked for rather than assumed: both
have been 4096 and 100 on every mainstream Linux for twenty years and neither
is guaranteed, `getconf` costs nothing in a batch that already forks, and
there is a default for the host that does not answer.

### Graphics cards

The one reading with no single place to be read from, and the asymmetry
decides where it sits. AMD exposes `gpu_busy_percent`, `mem_info_vram_*` and a
hwmon of its own under `/sys/class/drm/card*`, so an AMD card costs what any
other `/sys` read costs. NVIDIA exposes nothing usable in sysfs and everything
behind `nvidia-smi`, which is not a file read: it initialises a driver context,
typically hundreds of milliseconds and worse with persistence mode off. One
slow vendor is enough to keep the whole reading off the always-on tier.

Apple silicon is the third, and the one card that is soldered to a machine
Linqode may be running on itself. `ioreg -r -d 1 -w 0 -c IOAccelerator` needs
no root and costs **20 ms** — cheaper than nvidia-smi, and the reading is
`Device Utilization %` and `In use system memory` out of a plist-flavoured
node. It reports no VRAM total, because there is none: the memory is the
machine's, unified, and `hw.memsize` in that field would look like the
quantity the other two vendors put there and mean "share of system RAM"
instead. So the row shows what is in use and draws no bar for it. No
temperature either — Apple's die sensors are `PMU tdieN`, real readings with
no name saying which die — and no power draw, which needs root.

**What is asked for is the reading, not everything around it.** The ioreg node
is **46 KB** of bundle names, scheduler state and match dictionaries; a `grep`
for the three keys that matter leaves **497 bytes**. That is the same
discipline as `--query-gpu` on the NVIDIA side and named files on the AMD one.
The node boundary line is kept along with them, because the keys inside a node
come back in no order — `PerformanceStatistics` before `model` on this
machine — and a second accelerator would otherwise be unsplittable.

All three vendors are asked in a single exec, with the NVIDIA and Apple halves
behind `command -v`. This is the guard side of the probe's own rule: it
changes one command's fallback and nothing else, so it costs a shell builtin
rather than probe state. A host with none matches no glob and starts no tool —
**2 ms and 23 bytes**, measured. Intel is left out; it offers little without
something installed.

`/sys/class/drm` holds a directory per *connector* as well as per card, and a
connector's `device` symlink points back at the card, so `card0-DP-1` would be
read as a second card and double every number. Card names are matched exactly.

An AMD card's temperature appears twice — in the temperature row and in its
own — and that is correct: the driver registers its hwmon like any other chip.

### What the daemon is holding

`docker system df`, on the same gate at a thirty-second cadence. It answers
the question the filesystem rows raise and cannot: a `/var` at 93% says
nothing about how much of it is images nobody is running, and only the daemon
knows which layers are shared and which are dangling.

It is the one reading whose cost the fixture cannot demonstrate. Its 65 ms
here is a store of two images and no build cache; on a real host what the
daemon spends is the walk over the ones it does have. That uncertainty is
itself the reason it is on the on-demand tier rather than measured onto a
faster one.

It is not scoped to the project and cannot be — images, volumes and build
cache are the daemon's, shared with everything else on the host — which is
exactly what makes the answer worth having, since the thing filling the disk
is usually not this project's.

## The one machine read without a command

**The rule: one host is read one way, and only a local Mac is read
natively.** Which of the three applies is decided in `interactive.go`, from
what the probe found and whether the target is this machine:

| Target | Read by |
| ------ | ------- |
| any host with `/proc` — every Linux, remote or local | the batch, over the Executor |
| `local` on a host without `/proc` — a Mac | `gopsutil`, natively |
| any other host without `/proc`, over SSH | the batch, which still answers `df`; no process table |

The third row exists so that a native reader is never asked about a machine
it is not running on: it would answer about the operator's laptop and label
the numbers with the server's name. Watching a Mac over SSH is not a
supported target, and nothing here is built towards it.

**Why the second row needs a library at all.** What macOS lacks is not a file
but a **counter**. `sysctl kern.cp_time` does not exist there; `iostat -c 2`
blocks for a second and `top -l 2 -s 0 -n 0` for two-thirds of one, and both
then report percentages rather than the cumulative ticks the arithmetic in
`usage.go` subtracts. Read natively they are there —
`user=87320.9 system=39572.2 idle=2762182.4` — in exactly the shape that
arithmetic already consumes.

**Why the first row covers the local path too.** On Linux the local target
runs the same batch through the same Executor and produces the same numbers,
byte for byte. Reading `/proc` directly there would be faster and is
deliberately not done: two implementations that must agree eventually stop
agreeing — units, rounding, a filter's edge case — and only one of them would
be exercised by the e2e fixture.

Measured on an Apple M4, 10 cores, 16 GiB, best of five:

| Reading | Cost |
| ------- | ---- |
| whole sample, always-on tier | **50 ms** |
| — of which temperatures (41 sensors) | 50 ms |
| — filesystems, CPU ticks, memory, load | under 1 ms |
| — network interfaces | 3 ms |
| process table (609 processes, on demand) | 28 ms |

The temperatures are the whole cost, and they are on the always-on tier
because on Linux they are three files in a batch that is already being read.
Fifty milliseconds of local CPU every five seconds is one per cent of one
core of ten, spent on the machine doing the watching rather than on a server,
which is why it is left where it is rather than given a cadence of its own.

The process table has one deliberate omission. Its state — running, sleeping
— is a field of `/proc/<pid>/stat` and free on that path; here it shells out
to `ps` once per process, **2.0 s** for 684 processes against 28 ms for
everything else in that loop together. Nothing on the screen shows it.

Two readings a Mac gives that a server usually does not: real temperatures,
and a GPU. Two it cannot give: PSI, a Linux kernel concept, and the container
CPU/memory columns, whose cgroups live inside the Linux VM that Colima or
Docker Desktop runs and are not reachable from the host filesystem.

## Cost budget

Reproduce with:

```bash
go test -tags e2e ./tests/e2e/ -run TestRemoteCommandCost -cost.measure
```

| Command | Cost | Bytes |
| ------- | ---- | ----- |
| exec overhead (`true`) | 1 ms | 0 |
| capability probe (once, at connect) | 39 ms | 183 B |
| host batch (`/proc`, `df -Pk`, hwmon) | 6 ms | 2.8 KB |
| container cgroups (`/sys/fs/cgroup` + `/proc/<pid>/net/dev`) | 6 ms | 3.4 KB |
| process table (`/proc/<pid>/stat`, on demand) | 3 ms | 200 B per process |
| graphics cards (on demand, no card here) | 2 ms | 23 B |
| `compose ps --all --format json` | 65 ms | 6.2 KB |
| `docker system df` (on demand) | 65 ms | 116 B |
| `docker stats --no-stream` | 2.02 s | 973 B |

All measured **over loopback** against the e2e fixture, which is why they say
what a command costs on the server and nothing about what a real link adds.
That is what the sampler's stretch rule is for. The event stream is
deliberately absent: a stream has no round trip to measure, and what it costs
is the traffic it carries, which the watch tests exercise instead.

Host metrics therefore belong in the automatic refresh: their cost is noise
beside the `ps` already being paid, so they are always on. `host_metrics =
false` opts out for hosts where even that is unwelcome.

## Why agentless

Reconsidered explicitly in September 2026 and kept. Linqode **monitors when
the operator wants to monitor, and helps them investigate the host**. It is
not a monitoring system.

| | Where it samples | What crosses the wire | What must be installed |
| --- | --- | --- | --- |
| Tool on the server (`htop`, lazydocker over ssh) | on the host, free | a **rendered screen** | the tool, on every host |
| Agent + client (Netdata, node_exporter, Beszel) | on the host, free | a compact structured payload | a daemon, supervised, with a port |
| **Agentless over SSH (Linqode)** | on the client, after asking | command output | nothing |
| Remote API tunnelled over SSH (`DOCKER_HOST=ssh://`) | on the host for Docker data | structured JSON, streaming | nothing |

The first has a failure mode that is easy to miss: the wire carries an
*interface*, not data. A colour repaint of 80×24 is 10–20 KB, and every
keystroke costs a round trip before the cursor moves. At 200 ms RTT, `htop` on
the server is worse than a local dashboard sampling every three seconds — and
it would cost the local log engine, the machine interface, and every host you
cannot install on.

**The bottleneck was never SSH; it was the CLIs being driven.** `docker stats`
cost two seconds because the *daemon* reads each container's cgroups twice.
An agent would have been fast there not because it is local but because it
would read the cgroup files directly — which the client can do too, and now
does. After that, one sample is a shell fork, a handful of `cat`s, one round
trip and a few KB every five seconds.

What an agent would actually buy, once performance is off the table: history
while nobody is connected, alerting, and fleet. All three are non-goals
(see [PROJECT.md](PROJECT.md)). Reopen the question the day retention or fleet
is wanted; it will be a product decision, not a performance one.

### Root is not needed

Almost nothing worth reading requires it: cgroups, hwmon, PSI, `/proc`, the
container network counters — all world-readable. The two things that would are
SMART data and socket-to-process mapping, neither of which is on the screen.
If either is ever wanted, the answer is a `NOPASSWD` sudoers entry for those
specific commands, decided by the host's administrator — not a credential this
tool holds.

## Options recorded and not taken

- **A persistent remote sampler** — one long-running streamed command emitting
  a sample every N seconds. It would buy an agent's per-sample cost with
  nothing installed, and would matter on a 300 ms link. Costs: the cadence is
  fixed until it is restarted, errors are harder to attribute, and termination
  must be exact.
- **The Engine API over `docker system dial-stdio`** — the mechanism behind
  `DOCKER_HOST=ssh://`. It would remove the compose CLI's startup from every
  `ps` and give native event and stats streams. Less compelling than it was:
  the two costs that justified it, `docker stats` and the polling, are gone by
  other routes, and what remains is 65 ms once a minute. It is also a policy
  change — the principle today is "drive standard CLIs remotely and interpret
  their output locally" — so it is not something to slide into.

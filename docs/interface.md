# Interface

_Last updated: 2026-09-10._

How the screen is put together: what is drawn, what takes focus, and what
gives way when the terminal runs short. What is *read* to fill it is
[monitoring.md](monitoring.md); what the keys can *do* is
[operations.md](operations.md).

## The shape of the screen

```
┌─ linqode  user@host  /srv/app ──────────────────────────────────────────┐
│ host  cpu[▇▇▃░░ 28%]  mem[▇▇▇▇░ 3.1G/7.8G]  /var[▇▇░ 24G/98G]  52°C  up │
└─────────────────────────────────────────────────────────────────────────┘
┌─ services ──────────────────────── 4 running · 1 exited · 1 unhealthy ──┐
│ SERVICE  STATE    HEALTH   RESTARTS  CPU    MEM    NET RX/TX   IO R/W    │
│ api      running  healthy  0         2.1%   410M   1.2M/3M     0B/12M    │
└─────────────────────────────────────────────────────────────────────────┘
┌─ events ──────────────────────────────────────────────────── 6 events ──┐
│ 12:04:31  worker   killed (137)                                         │
└─────────────────────────────────────────────────────────────────────────┘
 tab panels · r refresh · x scripts · ! run · q quit │ enter logs · c actions
```

Three boxes and a **footer**. The first is the session and the machine it runs
on, the second the project, the third what happened to it — and each says one
thing, which is read faster than one that says two. The service counts used to
ride on the header's title line, which made the header about the session, the
machine *and* the project at once; they belong on the panel that holds the
services, and that is where they are.

Boxes touch. There is no blank row between them, because a border already
separates what it encloses from what is under it, and a blank row on top of
that would be a second separator stacked on the first.

### Anchor and satellites

The services table is the **anchor**: it fills the body and never shrinks
below what makes it a table. Everything else is a **satellite**, and a
satellite is the thing that gives its rows back when the terminal cannot hold
both.

This is not a grid. A 2×2 layout at 80×24 leaves each box about 36×7 of
content, which is narrower than the service table needs — the grid would
degrade the primary object to make room for secondary ones. btop can afford a
grid because it reads a local `/proc` for free and all its panels are graphs;
this cannot.

**A border must earn its cells.** Two columns and two rows per box is the same
cost that got an earlier right-hand sidebar removed, and every region that
takes focus earns them by naming which one the keys are talking to.

The header earns them for nothing, which is why it has them. It was a title
line and a bare row of meters, with a blank row under the pair to keep them
off the panel below — two rows spent on separating and labelling, which is
exactly what a border does. Turning them into one costs no height and buys the
header the same focus language as everything else: before it, the only sign
that the meters held focus was a four-letter label changing colour, against a
border and a title lighting up everywhere else.

Without a host sample there is nothing to put in the box, and the session falls
back to a bare line with a blank row under it. Still two rows, so nothing below
it moves.

**The title line names the session, and sometimes a fourth thing.** It is
`linqode`, the host, and the project directory — plus, in yellow, the docker
endpoint when it is not the host's own socket. That last one exists for the
local target: a session that inherited an exported `DOCKER_HOST` says `local`
and drives production, and this is the only place on the screen that would
say so. It is yellow because it is neither an error nor decoration.

```
┌─ linqode  local  ~/Dev/myapp  docker ssh://deploy@prod ─────────────────┐
```

A mount point too long for its column is cut at the **front**, at a segment
boundary where one fits: `…/Volumes/Data`. The head of a path is what two
rows have in common — `/System/Volumes/Data` and `/System/Volumes/Preboot`
are the same string for sixteen cells — and the tail is what tells them
apart.

### When the anchor has nothing to anchor

The [connect-time probe](monitoring.md) can find a host with no docker, a
refused socket, compose v1, or a `compose_dir` that moved. The screen is then
built around what is left rather than around what is missing.

The machine takes the body. Every reading the system view holds comes off
`/proc` and `/sys` and owes docker nothing, so on such a host the system view
*is* the anchor — the meters, the filesystems, the temperatures, the process
table, all of it — with the sentence about compose as its first row. What the
screen becomes is what it can honestly be, which is a machine monitor.

This was decided by looking at it rather than by reasoning about it. With the
table gone but still the anchor, a panel whose entire content was three lines
explaining its own absence held the largest area on the screen, with ten blank
rows beneath it, on a host that had every reading it always had.

Two exceptions. With `host_metrics` off as well there is nothing behind the
band either, and then the services panel keeps the body, because a panel that
at least says why is better than an empty machine. And the sentence is drawn in
**yellow, not red**, wherever it appears: nothing failed, the host is what it
is, and red is what the stale meters and the failed refreshes use.

Nothing that cannot work is offered. `enter logs`, `a live` and `c actions`
leave the footer; `x scripts` and `! run` stay, because neither ever needed a
daemon.

## Panels and focus

A panel is a titled box that occupies **exactly** the cells it was given:
content longer than its width is truncated and lines past its last row are
dropped, so a panel can never push its neighbours out of place. The chrome and
the size arithmetic live in `internal/tui/panel`; a feature package owns what
is inside its own panel and nothing beyond it, so adding a panel is adding a
panel rather than editing a screen.

`tab` and `shift+tab` move focus around the ring — band, table, events, in the
order they sit on the screen, so `shift+tab` from the table reaches the band
and `enter` there opens the machine's readings.

Focus starts at the **top**, on the machine, and the ring walks forward from
there. It used to start on the table, on the reasoning that the band is read
and the table is acted on; what that missed is that the machine is often the
reason the session was opened at all, and it sat two keystrokes away —
`shift+tab` backwards past the feed, then `enter` — while the table is one
`tab` forward and fills the body regardless.

The trade is real and worth naming: the arrows and `enter` do nothing until
that one `tab`, because the header answers none of them. It buys the machine a
keystroke and makes the ring walk forward from where it starts rather than
backwards.

Where `host_metrics` is off there is no header box and nothing at the top, so
focus starts on the table, which is then the only thing drawn.

**Focus never lands on a region that is not drawn**, and leaves one the
terminal shrinks under. Two of the three can be absent: the satellite gives up
its rows on a short terminal, and the machine has no header box on a host where
`host_metrics` is off. Moving the ring through them would change nothing on
screen and offer the keys of a region nobody can see.

The header box is drawn from the first frame, before any sample, saying it is
waiting rather than drawing numbers it does not have. One that appeared on the
first reading would push the whole body down a row a second after the screen
opened, and would make the ring's first stop — and the place focus starts —
exist only after a round trip.

The ring is advertised in the footer as `tab panels`, and it is the one thing
on this screen that has to be: every other key is either on the panel that
answers it or already on that line, while the band and the feed cannot be
reached at all without knowing the ring exists. It is the first hint dropped
when the line is short — learned once, then the least useful thing there — and
it is absent inside a detail view, where `tab` would move a focus nobody can
see, and wherever only one panel is drawn, where it moves nothing.

**Focus is carried by colour, never by reverse video.** Reverse already marks
the selected row inside a panel, and two marks that both mean "here" cancel
out. The focused panel takes the accent on its border and title; every other
one recedes to grey, and the selected row of an unfocused panel becomes a
quiet fill instead of a lit bar — still findable when focus comes back, no
longer competing with the panel that has it.

**Exactly one region is lit at a time**, which is what makes the accent mean
anything. The machine can be on screen twice — the band in the header, its
readings in the body of a host with no compose — and then only the body is
lit, because that is where the keys go. A detail takes the accent from the
header that opened it for the same reason.

A selected row is drawn **plain and then filled**. A background laid over text
that already carries its own colours ends wherever the first of them resets,
which showed up once as a highlight bar the width of a timestamp.

Colours are named explicitly rather than taken from the terminal's ANSI slots:
`lipgloss.Color("1")` does not ask for red, it asks for *this terminal's* red,
and the popular schemes deliberately desaturate those slots. Naming them costs
the ability to blend into a user's scheme and buys knowing what an operator
actually sees. Every token is adaptive, so the palette stays legible on a
light background.

### `enter` descends one level

On whatever has focus: the table into the selected service's logs, the band
into the system view, an event into the logs of the container it happened to.
`esc` comes back up. One concept, three destinations, and each is the obvious
next question about what has focus.

**`esc` only ever goes up, and `q` only ever leaves.** `esc` backs out of a
detail, a modal menu, the `!` prompt or the log view, and on the home — where
there is no level above — it does nothing at all. `q` quits from any of them.

Neither key does the other's job, which is the whole rule. `esc` used to quit
from the home, so in three places it meant "up one level" and in the fourth
"throw this session away", one keystroke apart and only one of them undoable.
`q` used to close a detail or a menu, which made it a second `esc` and left
the footer's own `q quit` wrong in every view that had one open.

The one exception is text: while the `!` prompt, a search, a filter or a field
name is being typed, every key types, so `q` is a `q` and `esc` cancels the
input rather than the view. A key being a character is self-evidently not a
command, and it is the only place either rule bends.

**Maximising a panel is a separate gesture and is deliberately unbound.**
Opening a detail changes context; making a box full-screen changes layout
only. With one anchor on the home it would buy nothing.

A detail and a modal menu take the *whole* body, satellites included. Opening
one changes what the screen is about, and leaving a feed running alongside
would be showing two contexts at once — which is the thing panels exist to
avoid.

**They take the focus ring with them.** `tab` and `shift+tab` do nothing while
a detail is open, and `enter` goes to the detail rather than descending again.
The panels those keys would move between are not on screen, so moving focus
among them changed nothing an operator could see and everything about where
`esc` landed — you opened the system view from the header and came back to the
table. `enter` was worse than surprising: it ran the descent a second time and
re-read the whole on-demand tier, three SSH round trips for a keystroke that
changed nothing.

The screen's own commands do go on working there — `r`, `x`, `!` — because
none of them is about which panel has focus. `c` is the exception, and for
exactly that reason: what it acts on is a selection, and the region that has
one is not the one on screen. See [the footer](#the-footer).

## The band

One row of meters inside the header box, always drawn, on every screen of the
home including the detail views. It is the first thing an operator reads and
the only reading on screen that is about the host rather than about a
container.

**It is a panel like the others.** It is the first stop in the focus ring,
`enter` on it opens [the system view](#the-system-view), its border and title
take the focus accent, and it owns its own rendering in `internal/tui/system`
— the same package, and the same sample, as the view behind it. The box's
title is the session rather than the word `system`, because that is what the
header is: which host this is, and how it is doing.

The `host` label stays inside, on the row with the meters, and still earns its
place: CPU and memory appear twice on this screen, once for the machine and
once per container below, and without the word the row reads as an aggregate
of the rows under it.

The layout follows htop's meters — the bar carries the percentage, the text
inside it carries the absolute amounts, so the percentage is never printed
twice. Bars share whatever the labels leave, stretch with the terminal, and
are capped at thirty cells because past that a gauge adds resolution nobody
reads. The label is the word `host`: CPU and memory appear twice on this
screen, once here and once per container below, and without the word the band
reads as an aggregate of the rows under it.

Which readings it carries follows from what they cost the row:

- **CPU** is the headline, and the load average stands in until there is one —
  a percentage is a difference between two samples, so it does not exist until
  the second arrives.
- **Memory**, always.
- **One disk meter, following the fullest filesystem**, labelled with its
  mount point. A comfortable `/` says nothing about the `/var/lib/docker` that
  is about to stop the deployment, which is among the most common ways one
  does.
- **Swap only once a meaningful share of it is in use.** Almost every healthy
  Linux machine has a little swapped out; a machine that is *filling* swap is
  in trouble `MemAvailable` does not show.
- **A temperature as a number, not a fifth gauge.** There is no width for one,
  and the colour carries the judgement a bare temperature cannot make for
  itself.

When the line runs short it sheds in order: uptime, then the temperature, then
meters from the bottom up. The staleness flag stays while anything is drawn at
all — a stale number that looks current is worse than a missing one.

## The system view

`enter` on the band opens it. The same sample with the room to print what one
row has to leave out, plus the readings that are only taken while it is open.

```
 cpu      [▇▇▇▇░░░░░░]  28% busy   load 7.21  5.98  4.55   over 8 cores   1m ▁▂▅▇▃▂
 cores    ▇▅▁▁▂▁▁▁   busiest cpu0 at 98%
 memory   [▇▇▇▇▇▇▇░░░]  24.6G used   6.7G available of 31.3G              1m ▃▄▅▆▇█
 swap     [▇▇▇░░░░░░░]  3.2G used   4.8G free of 8.0G
 /        [▇▇▇▇▇▇▇▇▇░]  89.0G used   107G free of 196G   /dev/nvme0n1p2
 /var     [▇▇▇▇▇▇▇▇▇▇]  372G used   27.6G free of 400G   /dev/sdb1
 docker   [▇▇▇▇▇▇░░░░]  37.94GB of 64.44GB reclaimable   images 48.21GB (25 idle)
 net                    283K/s down   70.8K/s up   busiest eth0 at 354K/s   1m ▂▇▃▁▄
 pressure               cpu 24.5%   io 8.3% (full 2.1%)   mem 0.0%   stalled 10s
 temp     [▇▇▇▇▇▇▇▇░░]  74°C Composite of 85°C   71°C Package id 0   44°C acpitz
 gpu      [▇▇▇▇▇▇▇▇▇░]  91% busy   20.9G/22.5G   74°C   149W   NVIDIA A10
 uptime                 42d7h
 system                 Debian GNU/Linux 12 (bookworm)

 processes  by memory   9 running
  process                    RSS       CPU   pid
  postgres                  11.0G     8.0%   9821
```

Rows are ordered by how much they answer "what is wrong with this machine",
because a short terminal truncates the box from the bottom. Every reading a
later feature adds lands here rather than in a new box on the home, which is
what keeps the home cheap to draw and cheap to sample.

- **The per-core strip** is one cell per core, not a labelled bar each: a
  labelled bar each stops fitting somewhere around sixteen cores, and the
  shape of the strip is what the row is read for. One pinned core among eight
  idle ones is a machine with a problem and an average that says twelve
  percent, so the busiest is named beside it.
- **The gauges shrink so the widest row fits.** The pressure row is the
  longest and the one with no gauge to give up, so the bars reserve what its
  text needs — which costs a wide terminal nothing, since the cap is reached
  either way.
- **The process list takes whatever is left** and draws nothing at all when
  there is room for fewer than two entries, since what it is read for is the
  top of it. `s` switches its ranking between memory and CPU: neither order
  answers the other's question — memory is who is holding the machine's RAM,
  CPU is who is burning it right now.
- **A card gets a row per card**, metered on utilisation with memory spelled
  out beside it, since on a card it is memory that stops work starting. Where
  a driver reports no utilisation the meter falls back to memory and the row
  says which number it is drawing. On Apple silicon there is no VRAM total to
  be a fraction of — the memory is the machine's — so the row shows what is in
  use and no share of it, which is the whole difference between the two:

  ```
   gpu      [▇▇░░░░░░░░]  21% busy   335M used   Apple M4 (10 cores)
  ```
- **The last row is what the machine calls itself**, established once by the
  [connect-time probe](monitoring.md) rather than sampled — it does not change
  and it never goes stale. It is last because the order is by urgency and the
  box truncates from the bottom: a host's name answers no question about what
  is wrong with it, so it is the row worth losing first. A host that reports
  no name draws no row, and nothing above it moves.

### Trend strips

Beside the CPU, memory and network rows runs a column of sparklines drawn from
samples already fetched — the one thing on the screen that costs the server
nothing, and the difference between "memory is at 88%" and "memory has been
climbing for ten minutes".

Each is scaled against **its own window**, not against 0–100. The meter beside
it already says the level; on a fixed scale, memory between 78% and 88% draws
as eight solid blocks and the climb inside it — the whole reason the strip is
there — disappears. The window is widened to a floor so a reading that never
moves is not drawn as if it had swung end to end, and colour still comes from
the raw value: **height says how it moved, colour says how bad.** Throughput
has no natural full and is scaled against the busiest moment in its window
instead.

The strips appear as a column or not at all. One showing up on a single row
would read as data about that row rather than as the terminal running out of
width, and they are the part the next sample can reconstruct — so they are what
a narrow screen gives up rather than the numbers. Their label says how much
host time they cover, measured from the samples that arrived rather than the
cadence that was asked for.

Nothing is retained across sessions. History while nobody is connected is what
an agent would buy, and that is a non-goal.

## The services table

The anchor. It sits in a titled box and draws its heading as a band across the
box's full width, the way htop and k9s do, so a table narrower than the
terminal reads as occupying its space rather than trailing off part way. The
gap between columns is the widest of four, three or two spaces whose layout
still fits, so a wide terminal spends its slack on breathing room and a narrow
one spends it on content.

Container readings arrive as columns — CPU, MEM, NET RX/TX, IO R/W — and
their sizes are printed as docker prints them. This is the daemon's own
accounting, and one screen showing two roundings of the same number is worse
than either.

**The service counts ride on its own rule**, set into the right end:
`4 running · 1 restarting · 1 exited · 1 unhealthy`, in lifecycle order rather
than alphabetical, because that is how an operator reads a project. On the rule
rather than in the footer because the footer shows only the panel that has
focus, and what a project is doing is worth seeing while looking at something
else. They are rendered with their own per-state colours and are therefore the
one label on a border that does not take the focus accent — a style laid over
text that already carries colours ends at its first reset. A rule too narrow to
hold them whole drops them rather than truncating: half of `1 unhealthy` is a
number beside a word that no longer says which state it counts.

## The events panel

A satellite under the table showing what the daemon reported, newest first.

It answers the question a table structurally cannot. A table is a statement
about *now* — `web` is running, it has restarted seven times — and what an
operator usually needs to know is *when* something happened and in what order.
It costs nothing extra on the wire, since the stream is already running for
the refresh, which is the only reason it earns a place at all.

Each line is **read rather than printed**: an exit code of 137 is a container
that was killed, and once the row is gone that is not visible anywhere else on
the screen. Containers are named the way the table names them, mapped back to
their service through the list the table already holds; an event about a
container the project no longer has keeps the name the daemon gave it.

The selection follows the newest event while it is on the newest event, and
stays on its own entry once it has been moved off — the log view's rule, and
for the same reason: something being read must not slide away.

It is laid out whenever the session has a stream at all, empty or not. An
empty feed says the daemon is being watched, which is worth knowing, and a
panel that appeared the first time a container died would move the table under
the operator at the worst possible moment.

**Its rule counts what it has caught, and says when nothing is listening.**
Losing the stream is not a failure — the table falls back to its timer, which
is a slower screen rather than a broken one — so `not watching` is dim, and it
is on the rule rather than in the footer because it is worth seeing whether or
not this panel has focus.

## The footer

**The footer is the keymap.** Two halves divided by a rule: **what works
wherever you are** on the left, **what the region with focus answers to** on
the right.

```
 tab panels · r refresh · x scripts · ! run · q quit  │  enter logs · a live · c actions
```

Before the split there were ten keys in one row and nothing to say which of
them would still work after pressing `tab`. Now the left half is the same on
every screen — learn it once — and only the right half changes under you. A
rule rather than another middle dot, because the two sides are different kinds
of thing and a dot would read as one list of ten.

**Which half a key belongs to is decided by what it acts on**: the session or
the machine on the left, a selection on the right. `r` reads everything again,
`x` runs a script the operator configured and `!` runs the line they typed —
none of them needs anything selected anywhere, and none of them needs a
daemon. `c` does: a lifecycle action is about one service. It sat on the left
until it was noticed that what it opened was always the table's selection,
whichever region had focus — a restart menu about a service chosen somewhere
else from the band, and, from the system view, about a service that was not on
the screen at all.

So **`c` is offered by the regions that have a service under the cursor**, and
by no others: the table, and the feed, where it acts on the container the
selected event was about rather than on whatever the table is sitting on. On
the band it is neither advertised nor answered, and inside the system view the
same. The cost is one `tab` for an operator who is reading the meters and
wants to restart something — the table is still on screen behind the band, and
its selection still visible — and the rule that buys it is the one anybody can
state after seeing the footer once. The alternative, "`c` works if the table
is *drawn*", is harder to learn than that `tab` is to press, and falls apart on
the host where the machine takes the body and there is no table at all.

It is the last hint on its side: `enter` is what the region is for, `c` is what
can then be done to what it selected. How readily it is given up on a narrow
line did not change with the side it is on.

**No reading appears here.** Service counts, event counts, uptime, core count,
the watching flag — each of those is monitoring, and monitoring belongs to the
region it is about, on that region's own rule, where it is legible without
focus and does not compete for the one line every key shares. The footer used
to carry all of them, and each was a weaker copy of something already two rows
above it.

What is left beside the keys is what has no other place: a refresh that
failed, a stats sample that did, a host with no compose. Those are not dim, and
they appear only when something is wrong — a status that is always there is
read as furniture, one that appears is read as a warning.

**What is given up when the line is short** is ordered by a `Drop` value on
each hint, highest first, and the two halves compete on that rather than on
which side they sit. `tab panels` goes first: it is learned once and then the
least useful thing there. `q quit` goes last. A status is never dropped — it
survives every hint, because a key can be rediscovered and an error cannot —
and only then is the line cut to the terminal rather than allowed to wrap,
which would push every row above it up by one.

A modal takes every key while it is open, so its footer says so: `q quit` is
the only thing left on the global side, with the menu's own keys on the other.
The `!` prompt has nothing global at all — every key types — so it has no
divider.

## Looking at it

The test suite runs without a TTY, where lipgloss drops every colour. That
leaves a class of defect no assertion catches because nobody can see it, so
**a change to how the interface looks is not finished until a frame has been
rendered in colour and looked at**. See [tests.md](tests.md), "Looking at the
UI". Defects that shipped and were obvious the moment a frame was rendered:
a gauge whose empty track took the same saturated colour as its fill, a
heading band shaded so close to the selected row that the two read as one
thing, a focus flag that was never passed, a process list drawing one row more
than it had been given, and every container column sitting empty because a
source that declined to read was left in flight forever.

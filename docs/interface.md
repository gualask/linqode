# Interface

_Last updated: 2026-09-05._

How the screen is put together: what is drawn, what takes focus, and what
gives way when the terminal runs short. What is *read* to fill it is
[monitoring.md](monitoring.md); what the keys can *do* is
[operations.md](operations.md).

## The shape of the screen

```
┌ linqode ─ user@host ─ /srv/app ── 4 running · 1 exited ──────────────────┐
 host  cpu[▇▇▃░░ 28%]  mem[▇▇▇▇░ 3.1G/7.8G]  /var[▇▇░░ 24G/98G]  52°C  up 12d
┌─ services ──────────────────────────────────────────────────────────────┐
│ SERVICE  STATE    HEALTH   RESTARTS  CPU    MEM    NET RX/TX   IO R/W    │
│ api      running  healthy  0         2.1%   410M   1.2M/3M     0B/12M    │
└─────────────────────────────────────────────────────────────────────────┘
┌─ events ────────────────────────────────────────────────────────────────┐
│ 12:04:31  worker   killed (137)                                         │
└─────────────────────────────────────────────────────────────────────────┘
 6 services  ·  enter logs · a live · r refresh · c actions · x scripts · q
```

A **title line**, a **band**, a body of **panels**, and a **footer**. A blank
line separates the header from the body rather than a rule, because the panels
draw their own borders and a rule would be a second separator stacked on the
first.

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
cost that got an earlier right-hand sidebar removed, so only a region that can
take focus draws one. A purely informative region stays a bare band.

## Panels and focus

A panel is a titled box that occupies **exactly** the cells it was given:
content longer than its width is truncated and lines past its last row are
dropped, so a panel can never push its neighbours out of place. The chrome and
the size arithmetic live in `internal/tui/panel`; a feature package owns what
is inside its own panel and nothing beyond it, so adding a panel is adding a
panel rather than editing a screen.

`tab` and `shift+tab` move focus around the ring — band, table, events. Focus
skips a satellite that is not currently drawn, and leaves one if the terminal
shrinks under it.

**Focus is carried by colour, never by reverse video.** Reverse already marks
the selected row inside a panel, and two marks that both mean "here" cancel
out. The focused panel takes the accent on its border and title; every other
one recedes to grey, and the selected row of an unfocused panel becomes a
quiet fill instead of a lit bar — still findable when focus comes back, no
longer competing with the panel that has it.

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

**Maximising a panel is a separate gesture and is deliberately unbound.**
Opening a detail changes context; making a box full-screen changes layout
only. With one anchor on the home it would buy nothing.

A detail and a modal menu take the *whole* body, satellites included. Opening
one changes what the screen is about, and leaving a feed running alongside
would be showing two contexts at once — which is the thing panels exist to
avoid.

## The band

One row, always drawn, on every screen of the home including the detail views.
It is the first thing an operator reads and the only reading on screen that is
about the host rather than about a container. It takes focus like a panel
without a border it has no room for: its `host` label carries that instead.

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
  says which number it is drawing.

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

## The footer

The focused panel's status on the left, then the keys. Inside a detail the way
out comes first — the panel's way *in* says `enter`, which is the key just
pressed — followed by whatever the detail itself answers to, then the screen's
own commands, which go on working there.

Hints carry a drop order and are shed until the line fits, so the two sets
compete on urgency rather than on which was listed first. The last hint
standing is kept whatever the width: a footer of one thing tells an operator
more than a footer of nothing.

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

# Interface

_Last updated: 2026-09-15._

How the screen is put together: what is drawn, what takes focus, and what
gives way when the terminal runs short. What is *read* to fill it is
[monitoring.md](monitoring.md); what the keys can *do* is
[operations.md](operations.md).

## The shape of the screen

```
┌─ linqode  user@host  /srv/app ──────────────────────────────────────────┐
│ host  cpu ▇▇▃░░ 28%   mem ▇▇▇▇░ 3.1G/7.8G   /var ▇▇░ 24G/98G   52°C  up │
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

The accent reaches one more place: **the keys of the focused half of the
footer**, so the lit border at the top and the keys at the bottom are visibly
the same statement. See [the footer](#the-footer).

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
twice. What the bar has not filled is the same track a chart's columns stand
in; the `░` in the sketches above is a plain-text stand-in for it — and
what is actually drawn on a terminal that takes no colour (`NO_COLOR`,
`CLICOLOR=0`, `TERM=dumb`). The track is a background, a background is a
colour, and without one it would be bare spaces: a gauge with no end. Sixteen
colours are enough for the fill.

**No brackets around the gauges.** htop writes its meters between them and
needs to: its track is empty space, and without the `]` nothing says how far
the bar could have gone. A bar standing in a fill ends where the fill ends,
so the brackets were two cells per meter spent saying it twice — and on the
band, where four meters share a row, the fill groups a label with its own
amount as well as the bracket did. They came off everything that draws a
gauge in September 2026: the band, the readings, the docker disk rows. Bars share whatever the labels leave, stretch with the terminal, and
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
row has to leave out, the history of the readings that move, and what the
machine is running — the last two read only while it is open.

It is **about the machine and nothing else**. What docker holds on disk was a
row here until September 2026, and is under the services table now, with the
rest of what is about docker.

**It fills the screen in both directions**: the readings at the top, in two
columns where the terminal is wide enough; a row of short charts under them;
and the processes in whatever is left, both rankings side by side where the
width allows.

```
 cpu      ▇▇▇░░░░░░░  28% busy   load 7.21  5.98  4.55   over 8 cores    net                  283K/s down   70.8K/s up   busiest eth0 at 354K/s
 cores    ▇▇▅▅▁▁▂▂▁▁  busiest cpu0 at 98%                                pressure             cpu 24.5%   io 8.3% (full 2.1%)   mem 0.0%   stalled 10s
 memory   ▇▇▇▇▇▇▇░░░  24.6G used   6.7G available of 31.3G               temp     ▇▇▇▇▇▇▇▇░░  74°C Composite of 85°C   71°C Package id 0
 swap     ▇▇▇░░░░░░░  3.2G used   4.8G free of 8.0G                      gpu      ▇▇▇▇▇▇▇▇▇░  91% busy   20.9G/22.5G   74°C   149W   NVIDIA A10
 /        ▇▇▇▇▇▇▇▇▇░  89.0G used   107G free of 196G   /dev/nvme0n1p2    uptime               42d7h
 /var     ▇▇▇▇▇▇▇▇▇▇  372G used   27.6G free of 400G   full in ~1h       system               Debian GNU/Linux 12 (bookworm)

 cpu  28% busy                    3m     memory  79% used                 3m     net down  283K/s   peak 354K/s   3m     net up  70.8K/s   peak 96.1K/s   3m
                 ▄██▄                                                                  █           ▄                                   █
                ▄████▄                   ▄▄▄▄▄▄▄▄▄▄▄█████████████████████████          █▄          █          █             ▄         ██                   ▄
               ▄██████▄                  ████████████████████████████████████         ███         ▄██         █▄            █         ██▄         █        █
 ▄▄▄▄▄▄▄▄▄▄▄▄▄██████████▄▄▄▄▄▄▄▄▄▄▄▄▄    ████████████████████████████████████    ▄▄▄▄▄████▄▄▄▄▄▄▄▄███▄▄▄▄▄▄▄▄▄██▄▄▄▄▄    ▄▄▄██▄▄▄▄▄▄▄▄███▄▄▄▄▄▄▄▄▄█▄▄▄▄▄▄▄▄█▄

 processes  by memory   312 running                                     processes  by cpu   312 running
  process                    RSS       CPU   pid                         process                    RSS       CPU   pid
  postgres                  11.0G     8.0%   9821                        java                       3.2G     41.2%   2214
```

### The readings

Two columns from 196 cells, one below that. **The first column is what fills
up** — processors, memory, swap, disks — and the second what the machine is
doing and what it is: network, pressure, temperatures, cards, uptime, its
name. Read top to bottom, one column is the order the view always had, which
is by how much a row answers "what is wrong with this machine", because a
short terminal truncates the box from the bottom.

**The rows are there from the first sample.** Three readings here are
differences between two samples — the CPU share, the per-core strip, both
throughputs — and their rows used to arrive one sample after the view opened,
pushing everything under them down the screen as they did. The row is drawn
either way and says `—` until it has a number, except the CPU meter, which
has something better to stand in with: the load average, under its own label,
until the share exists.

- **The per-core strip** draws the cores as heights, not as a labelled bar
  each: a labelled bar each stops fitting somewhere around sixteen cores, and
  the shape of the row is what it is read for. One pinned core among eight
  idle ones is a machine with a problem and an average that says twelve
  percent, so the busiest is named beside it. It stands in the same track as
  the gauges and is as wide as they are — with room to spare a core takes
  several cells, and on a machine with more cores than cells each cell
  carries the busiest of the ones it covers, since a strip that showed every
  other core would be the one way to lose the reading. It was one cell per
  core on the bare background until September 2026, which left it the single
  shape in that column with no track under it.
- **The gauges shrink so the widest row fits** its column. The pressure row
  is the longest and the one with no gauge to give up, so the bars reserve
  what its text needs — which costs a wide column nothing, since the cap is
  reached either way.
- **A card gets a row per card**, metered on utilisation with memory spelled
  out beside it, since on a card it is memory that stops work starting. Where
  a driver reports no utilisation the meter falls back to memory and the row
  says which number it is drawing. On Apple silicon there is no VRAM total to
  be a fraction of — the memory is the machine's — so the row shows what is in
  use and no share of it, which is the whole difference between the two:

  ```
   gpu      ▇▇░░░░░░░░  21% busy   335M used   Apple M4 (10 cores)
  ```
- **The last row is what the machine calls itself**, established once by the
  [connect-time probe](monitoring.md) rather than sampled — it does not change
  and it never goes stale. It is last because the order is by urgency: a
  host's name answers no question about what is wrong with it, so it is the
  row worth losing first. A host that reports no name draws no row, and
  nothing above it moves.

### The charts

CPU, memory, and the machine's throughput each way, drawn from samples already
fetched — the one thing on the screen that costs the server nothing, and the
difference between "memory is at 88%" and "memory has been climbing for ten
minutes".

They used to be **a column of strips one row high** beside the readings. One
row is eight heights to draw a history with; the column vanished altogether
wherever the widest reading left fewer than twenty-four cells, which on a
host with a GPU row was most terminals; and the screen under the process list
stayed empty. The charts took that room instead — and then took rather more
of it than they were worth, on the argument that height buys resolution: a
share drawn from nought to a hundred over ten rows is eighty heights, and
memory going from 78% to 88% is eight of them. What that bought in practice
was a wall, twelve rows in which a busy machine is a solid block of colour and
an idle one is empty air, over a process list squeezed to make room for it.

**A chart is five rows**: a title and four rows of history, four rows where
the terminal is short. The chart is read for *when* something happened; how
much it is right now is what the meter above it is for.

**The history is drawn as solid columns**, a sample each and touching, over
the track — which is the meter's shape stood on end. Four rows of the finer
drawing is a ragged edge of eighths, and columns with air between them read
as a grid rather than as a history; they had that air for an afternoon and
it is gone.

**The columns are there before the readings are.** A trend needs two
samples, and the charts used to appear one sample after the view opened,
pushing the process list down the screen as they arrived. Every column is
drawn from the first frame instead — a quiet fill a shade off the terminal's
background — and the readings fill them from the right, so a chart has its
shape before it has its history. A reading that needs a difference, which is
the CPU share and both throughputs, says `—` until it has one.

**A gauge and a chart stand in the same track.** There is one empty in this
interface and it is a quiet fill a shade off the terminal's background: the
half of a meter that is not used, the rows of a chart that are not filled
yet, the part of a log level's bar that the other levels hold. Every shape
`spark` draws stands in it — the band's meters, the readings, the per-core
row, the charts, the docker disk rows and the bar the log panel divides
between levels — and they are the same colour and read as the same thing. The one exception is the live panel's strips, which
are drawn on the bare background. The meter's track was a field
of `░` until September 2026, which put two kinds of empty one above the other
the moment the charts landed under the readings — a speckled one in the
gauges, a filled one in the charts. The speckle lost: it had already been
found to shout as loudly as the fill when it took the reading's own colour,
and a chart's track is four rows of it.

**A share is drawn from nought to a hundred**, so the height is the level, as
it is on the meter above. A rate has no natural full and is drawn against the
busiest moment the chart holds, which its title names as the peak.

**Colour says how bad**: a share's columns are coloured by their own reading,
so a climbing memory chart turns yellow where the reading did. Throughput is
one colour, because the top of a rate chart is only the top of what happened.

**The title says how far back the chart reaches**, at its right end. That was
an axis row under the history until the charts got short enough for the axis
to be a fifth of one, and every chart on the screen covers the same minutes
anyway. The newest column is against the right edge, so a chart that has not
filled yet fills from the right. The history holds an hour, so a wide
terminal reaches further back than a narrow one.

Four charts share a row where each gets 36 cells, wrap into two rows of two
where they do not, and into one column below that — never three over one,
which reads as a fourth that did not fit.

Nothing is retained across sessions. History while nobody is connected is what
an agent would buy, and that is a non-goal.

### The processes

**Both rankings are on screen where the width allows it**: by memory on the
left, by CPU on the right. Neither order answers the other's question —
memory is who is holding the machine's RAM, CPU is who is burning it right
now — so where there is room for both there is no reason to make an operator
switch. Below that width it is one list, and `s` switches its ranking; with
both on screen `s` is neither offered nor answered.

**The list has no limit of its own.** It stopped at twelve on the grounds that
past that it was a worse version of a table; what it was in practice was
twelve rows over an empty screen, and the reading behind it had already
fetched every process there is.

### What gives way

The readings are drawn first, and what they leave is divided between the
charts and the processes in one place. The charts ask for a fixed few rows —
five, or four where that is all that fits — and everything past them belongs
to the processes. There is nothing left to negotiate: the charts used to take
45% of the room and then grow into whatever rows the process list could not
fill, which is how a screen of four charts twelve rows tall over nine
processes happened.

When only one fits, **it is the processes**. The numbers before the shapes:
the next sample reconstructs a chart, and nothing reconstructs which process
was on top. Below that there is neither, and below the height of the readings
themselves it is the panel's box that cuts them, from the bottom.

### When a disk will be full

A filesystem row says **`full in ~40m`** when the history says it is filling
fast enough for that to matter. The meter says how full a disk is; what it
cannot say is whether 93% is where `/var` has sat for a year or where it
arrived this morning, and only the second one is the deployment about to
stop.

The rate is a least-squares slope over the remembered samples rather than the
difference between the first and the last, so one large write at either end
does not decide it. The sentence is said only on evidence that can carry it:
six samples across at least a minute, growth beyond what `df`'s kilobytes
could mis-round, and a fill time within a hundred times the window it was
measured over and never past a day. Ten minutes of growth say something about
the next few hours and nothing about next week, when a log rotation will have
happened. It is red inside the hour and yellow beyond it, and there is no
green: a projection appears only when there is something to say.

It sits before the device name, which is the part of the row a narrow
terminal can best spare.

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

**What docker holds on disk is under the table**, when the table leaves room:
the share the daemon would give back on the section's rule, and a row per kind
with a meter for its reclaimable share, the amount, and how many are idle.

```
 ── docker disk · 37.94GB of 64.44GB reclaimable ──────────────────────────
 images         48.21GB  ██████████████░░░░░░    31.42GB reclaimable   25 idle of 31
 containers     1.204GB  ███████░░░░░░░░░░░░░    402.7MB reclaimable   8 idle of 12
 volumes        8.914GB                       nothing to reclaim   all 4 in use
 build cache    6.117GB  ████████████████████    6.117GB reclaimable   118 idle of 118
```

It used to be a row of the system view, which put a question about docker in
a view about the machine. Here it is with the project's other docker
readings, and it takes **only rows the table leaves empty**: a project long
enough to fill the panel keeps every row, and the section — and the reading
behind it — waits for a terminal with room. A volume store entirely in use
says so rather than showing an idle count of nothing, because a store that is
all in use must not read as one waiting to be cleaned.

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

**Its keys are offered only when there is something under the cursor**, which
on this panel is most often not the case: see [the footer](#the-footer).

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
same.

**Under the cursor, not merely in the region.** Both regions offer their keys
— `enter logs` as well as `c actions` — only while there is something under
the cursor, and offer nothing at all otherwise: that half of the line goes
empty, divider included.

For the feed that is an empty feed, which is not a panel waiting for data but
a deployment where nothing has happened — the good case, and the one an
operator is in most of the time — or an event about a container that was
destroyed, which is a row and a cursor with nothing behind them.

For the table it is a compose project with no containers at all. `ps` is asked
with `--all`, so a project that is merely *down* still lists its services as
exited and `start` is exactly what they are waiting for; no rows means nothing
has ever been created here, and the only command that would help is a
project-level `up`, which Linqode does not have (see
[PROJECT.md](PROJECT.md#decided-policies), the mutation surface). What the
panel offers instead is the sentence `(no services in this compose project)`
where the rows would be, which is worth more than three keys that do nothing.

The cost is the first round trip of a session, where the table says it is
loading and its keys are not on the line yet. They arrive with the rows they
are about. The cost is one `tab` for an operator who is reading the meters and
wants to restart something — the table is still on screen behind the band, and
its selection still visible — and the rule that buys it is the one anybody can
state after seeing the footer once. The alternative, "`c` works if the table
is *drawn*", is harder to learn than that `tab` is to press, and falls apart on
the host where the machine takes the body and there is no table at all.

It is the last hint on its side: `enter` is what the region is for, `c` is what
can then be done to what it selected. How readily it is given up on a narrow
line did not change with the side it is on.

**The key is marked, the word is not** — `c` is lit and `actions` stays
recessive — and the mark differs between the halves: the always-available keys
are body text, the focused region's keys take the focus accent. Three levels,
one colour.

This is what makes the split visible rather than merely true. Both halves used
to be one flat grey, so `tab` swapped three phrases inside a row of nine and
nothing said which three: an operator had to read the line word by word to
find out that anything had happened at all. Now the pattern of marked
characters differs per region and registers before anything is read, and the
accent is the one already worn by the border of the panel that changed — the
two ends of the screen say the same thing, which is the only answer available
to the real complaint here, that what lights up is at the top and its keys are
at the bottom.

The global keys are body text, and **body text here is the terminal's own
foreground rather than a colour of ours that resembles it**: nothing is set,
so the key is whatever the operator's palette calls text, in a light theme, a
dark one or a themed one. What makes it stand out is the dim receding from it,
which is the same relationship everywhere.

Marking them a step above their words instead — bright enough to find, quiet
enough to stay furniture — was rendered alongside and was the more disciplined
line on paper: it would have left the accent as the only lit thing down there.
Looking at the two settled it the other way. The half that never changes is
the half being read while learning the application, and a key spelled at the
same brightness as the data is the one you find without hunting; the cost is
that the footer carries a little more weight than it used to, which is a cost
paid once per screen rather than once per glance. **These questions are
settled by rendering the candidates and looking at them**, not by reasoning
about them (see [tests.md](tests.md), "Looking at the UI").

**The log view marks its keys the same way**, for the same reason a keymap
spelled two ways is one learned twice. It has no divider — the whole screen is
one region, so everything there is that region's — but `q quit` keeps the
global grey even so: a key that works from anywhere must not change colour
depending on which screen it is read from. What is *not* marked there are the
prompts: `empty clears` has no key in it, and a first word dressed as one
would be advertising something nobody can press.

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

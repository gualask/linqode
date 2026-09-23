# Operations

_Last updated: 2026-09-10._

What Linqode *does* to a host, as opposed to what it reads from one
([monitoring.md](monitoring.md)) or draws on the screen
([interface.md](interface.md)).

Two things, and the line between them is the point: **Linqode drives the
compose lifecycle, and it runs the shell scripts the operator configured.**
Everything else an operator might want to do on that host is what the `!`
prompt is for, and that is a human capability, not a general one.

## The capability boundary

`internal/operations` owns every remote workflow. The TUI and the machine
adapter both call it; neither reconstructs commands nor owns service or
script validation.

What it exposes is **typed**: observation, compose lifecycle, and
configured-script methods. It does not expose arbitrary execution. The one
arbitrary path is `AdHoc`, and only the TUI is given it, behind the human `!`
prompt.

That split has a second half, in where things run. **What Linqode builds is
project-relative and gets a compose-dir `cd`; what the user supplies runs
where `ssh host 'command'` would run it** — the same login directory as any
other remote shell command. A configured script is the user's, so it runs in
the user's place.

## Service actions

Restart, stop and start on the selected service build the corresponding
`docker compose` command against the project directory.

**Which service is selected is the focused region's business.** `c` is offered
by the regions of the home that have a service under the cursor — the table,
and the events feed, where it acts on the container the selected event was
about — and by no others. On the host band and inside the system view it is
neither advertised nor answered: those have no selection, and a menu opened
from them named whatever the table happened to be sitting on, which from the
system view is not even on the screen. The rule and what it costs are in
[interface.md](interface.md#the-footer); the menu itself still belongs to the
screen, since it takes every key while it is open.

The actions live behind a menu (`c`) rather than on a key each, because of the
keymap rule: **no two keys in the application differ only by the shift key.**
`s` for stop beside `S` for start puts a production service one mistyped
capital away from the opposite outcome. Showing the exact command before
running it falls out of the same design — the menu has to render something,
and the command is the honest thing to render.

One exemption survives, and only one. `n`/`N` — next and previous search match
in the log view — is a case pair because "previous" has no arrow of its own,
and getting it wrong moves the cursor rather than a service. The vim aliases
that used to sit beside it are gone: `j`/`k`, `g`/`G` and `l` were second
spellings of keys every terminal already sends, so what they bought was a
keymap to be learned twice. Navigation is the arrows, `PgUp`/`PgDn` and
`Home`/`End`, and it is the same set in every list on the screen.

## Scripts

Named shell commands from the TOML config, listed behind `x`. This is the
half of the product that is not monitoring: the operator writes what their
deployment needs — a backup, a migration, a cache flush — and Linqode gives it
a name, a confirmation, and a live view of its output.

Scripts are resolved by configured name. The machine interface can run them
and passes **no runtime arguments**: what runs is exactly what the config
says, which is what makes the boundary safe to hand to an agent.

## Ad-hoc commands

`!` opens an inline footer prompt that runs its line verbatim on the host. It
is available only through the TUI, never through the machine interface.

It reopens holding the last command, so a typo is corrected rather than
retyped — the same courtesy the log view's `/` and `f` offer.

## Modal input

Menus and the prompt are both modal: while one is up, every key goes to it
rather than to the table, so `q` types a `q` instead of quitting.

- **Menus** (`c`, `x`) share one type: a list of entries, each a label plus
  the command it runs, navigated with the arrows and dismissed with `esc` or the
  key that opened it. `x` is always there; `c` is offered only where something
  is selected for it to act on.
- **The prompt** (`!`) is an inline footer input: every key edits the line.

## One feed for all of them

Actions, scripts, ad-hoc commands and log follows all produce the same
presentation-neutral stream of typed events. The TUI maps it into the full
screen follow view, shows the exit code, and refreshes status on return; the
machine adapter maps it into JSON Lines.

### Following logs

Opening a service starts `docker compose logs --follow` — no prefix, no
colour, tailing recent history — on a streaming exec channel. From there:

1. the shared stream reassembles complete lines across arbitrary chunk
   boundaries and emits typed events through a buffered channel;
2. the log view drains that channel every 100 ms, with an upper bound per
   tick so a log burst cannot starve input handling, into a bounded tail
   buffer that parses each line on entry;
3. the view renders the visible slice, following the tail until the user
   scrolls up. Jumping to the bottom re-enters follow mode.

Search (`/`, then `n`/`N`) runs over the visible lines with wrap-around and
match highlighting. Closing the view cancels the remote command — terminate
signal, then channel close — and tears down the pipeline.

The log engine is deliberately **Docker-agnostic**: it consumes generic
streams of bytes and lines, so it can later be pointed at plain files
(`tail -F` over SSH) or any other remote command without changes.

### Structured logs

Every incoming line is offered to the JSONL parser; a line that is a JSON
object becomes a record whose nested fields are flattened to dotted paths
(`http.status=500`). On top of these records:

- **Detection**: when most buffered lines parse as records, the view switches
  to structured rendering — timestamp, coloured level, message, then remaining
  fields — with a manual override either way (`s`). Well-known level, message
  and timestamp key variants are recognised.
- **Filters** (`f`): `key=value` / `key!=value` terms, AND-ed and
  case-insensitive, narrow the visible view to matching records; plain-text
  lines are hidden while a filter is active. Scroll, search and follow all
  operate on the filtered view, whose indices stay consistent across buffer
  drops by sequence-number accounting.
- **Stats** (`a`): counts by level and top values of a chosen field (`t`), in
  a side panel, recently and in all (see [Recent counts](#recent-counts)).
  They are recomputed on demand over the bounded tail (see
  [porting.md](porting.md), deliberate divergences). The panel is cut to the
  log's height from the bottom, where the least frequent values are, so a
  short terminal keeps its footer.

### Recent counts

A count alone cannot say whether the errors are still happening — the
difference between an incident and a scar. So every row carries two numbers:
**how many in a window ending now, and how many in all**.

**The window follows the pace of the view**: the shortest of 1m, 5m, 15m, 1h,
6h and 24h holding at least a tenth of the lines that can be placed in time.
A fixed window would be all noughts on a quiet service and the whole tail on
a busy one; a tenth rather than a half, because a window holding most of the
tail repeats the total. The panel names it (`last 5m · by log time`). Once
chosen it **holds** until it keeps under half that tenth, so a bursty service
does not flip windows every frame; a shorter window is taken as soon as it
qualifies, and a filter starts the choice over.

**The levels are also drawn as one bar**, a segment per level, worst first,
so red always starts at the left edge. It answers what a count cannot: five
errors is a sliver of a busy service and the whole of a quiet one. A share
above zero always gets at least one cell, borrowed from the widest segment.
The rows under the bar follow its order; field values stay ordered by count.

**The counts are of the lines in view**: a filter narrows them, and the panel
says what it counts out of (`5 of 190 lines`). The window is chosen over the
filtered lines too, so windows with and without a filter are not comparable.

This replaced a histogram of when the lines were written (removed September
2026): height for how many, colour for the worst level — two readings in one
shape, neither of them a number.

**One clock places the whole view**, never a mix, and the panel names it:

- **By log time** when at least half the lines carry a readable timestamp. A
  line without one — the traceback under an error — takes the time of the
  record before it. A timestamp is read only when it carries a zone, or is
  an epoch number (s, ms, µs or ns) landing between 2000 and 2200; one
  without a zone is left unread, since a wrong guess moves every record by
  hours.
- **By arrival** otherwise. The backlog a follow opens with was written over
  hours and arrives in one burst, so it is **not placed**: it counts in the
  totals, never as recent. Docker marks no boundary between replay and live
  lines, so the burst ends at the first drain (100 ms) that finds nothing
  new, or after two seconds. Only log follows have a backlog; script and
  command output is live from its first line. `docker compose logs
  --timestamps` would stamp every line, and was not taken: thirty bytes more
  per line, and the engine stays Docker-agnostic, so scripts and `tail -F`
  need the arrival clock anyway.

A line stamped in the future — a server clock slightly ahead — is recent
under every window.

## The machine interface

The command reference and the output/exit contract live in the
[README](../README.md#machine-interface). What matters here is the shape of
the boundary.

After strict local selection, the composition root creates one SSH session and
hands `internal/cli` a narrow safe interface implemented by the same
`HostOperator` the TUI uses. One-shot status and stats become versioned JSON
documents; followed stats, logs, lifecycle actions and scripts project the
shared feed into JSON Lines.

Remote stderr is an event in that stdout stream; Linqode's own typed errors
use stderr. Read failures use Linqode exit codes, while a started lifecycle
action or script propagates the reported non-zero remote status. **No command
is retried or reconnected automatically** — a mutation whose outcome is
uncertain must not be repeated by a machine.

One probe runs after connecting, before the operation. A command that needs
compose on a host that cannot run it is rejected with the condition named
rather than left to exit non-zero with whatever the shell printed:
`docker_permission_denied` is an account to add to a group,
`docker_unavailable` a host to install docker on, `compose_dir_missing` a path
to correct in the config, `compose_unavailable` what is left. A probe that
fails, or that establishes nothing, rejects nothing — the command runs and
reports its own failure, which is never worse than before. `script` is exempt:
it is a command the operator wrote and has never needed a daemon.

Machine commands accept only exact host and script names from the default
operator-controlled TOML. Inline targets, `--config`, arbitrary execution and
runtime script arguments are unavailable, and authentication never prompts or
learns an unknown host key.

**A local host is configured and still outside the boundary.** Its value is
the gap between what an agent can do without Linqode — nothing on that server
— and what Linqode grants it; on the machine Linqode runs on that gap is
zero, because the agent already has a shell there. So a host whose `host` is
`local` is refused with `local_host` rather than `unknown_host`, which would
send a caller hunting for a typo, and `hosts` omits it: that command is this
surface's discovery, and listing a name every other command here refuses
would be a lie. `config.toml` therefore holds two categories of host, those
an agent may reach and those only the operator may.

**Which TOML that is comes from the environment**, and so do the credentials:
`HOME` locates `~/.config/linqode/config.toml` and `~/.ssh`, `SSH_AUTH_SOCK`
the agent. That is a stated precondition rather than a hole — the boundary is
a capability guardrail over an environment the operator controls, and an
environment somebody else controls is one where they can run the command
themselves. See the boundary policy in [PROJECT.md](PROJECT.md).

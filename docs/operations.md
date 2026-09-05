# Operations

_Last updated: 2026-09-05._

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

They live behind a menu (`c`) rather than on a key each, because of the
keymap rule: **no two keys in the application differ only by the shift key.**
`s` for stop beside `S` for start puts a production service one mistyped
capital away from the opposite outcome. Showing the exact command before
running it falls out of the same design — the menu has to render something,
and the command is the honest thing to render.

The exemption is the bindings Linqode did not invent. `j`/`k`, `g`/`G` and
`n`/`N` are vim and less conventions users already have in their fingers, and
getting one wrong moves the cursor rather than a service.

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
  the command it runs, navigated with `j`/`k` and dismissed with `esc` or the
  key that opened it.
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
  a side panel. They are recomputed on demand over the bounded tail (see
  [porting.md](porting.md), deliberate divergences).

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

Machine commands accept only exact host and script names from the default
operator-controlled TOML. Inline targets, `--config`, arbitrary execution and
runtime script arguments are unavailable, and authentication never prompts or
learns an unknown host key.

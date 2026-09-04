package compose

// The daemon's event stream, narrowed to the project and to what actually
// changes a service's row.
//
// This is what the status view watches instead of asking `ps` on a timer. An
// idle deployment produces nothing at all, so the steady-state cost of having
// Linqode open drops to the stream itself; a container that dies is on screen
// as soon as the daemon says so, rather than within the next interval.

import "strings"

// watchedActions are the events worth re-reading the table for. Everything
// else the daemon reports is either about another kind of object or about
// something the table does not show.
//
// The list is explicit, and the filtering happens on the server, because the
// alternative is expensive in a way that is easy to miss: health checks emit
// `exec_create`, `exec_start` and `exec_die` for every probe of every
// container. Measured against a single container probing every two seconds,
// those were thirty of the thirty-seven events in ten seconds — a stream that
// would cost more bandwidth on an idle project than the five-second `ps` it
// replaces.
var watchedActions = []string{
	"create", "start", "die", "stop", "kill", "restart", "destroy",
	"pause", "unpause", "rename", "update", "oom", "health_status",
}

// eventFormat keeps each event to about thirty bytes: the action, the
// container it happened to, and its exit code when it has one. The default
// `{{json .}}` carries every compose label the container was created with,
// which is a kilobyte per event to say "web restarted".
//
// Two details, both measured rather than assumed. `index` rather than a field
// lookup, because a missing attribute yields the zero value instead of the
// literal `<no value>`. And a pipe rather than a tab, because docker does not
// interpret `\t` in a format string — it prints the two characters — while a
// real tab would be an invisible control character in a command that error
// messages quote back. No field can contain a pipe: container names are
// restricted to word characters, and the actions are the daemon's own.
const eventSeparator = "|"

const eventFormat = `{{.Action}}` + eventSeparator +
	`{{index .Actor.Attributes "name"}}` + eventSeparator +
	`{{index .Actor.Attributes "exitCode"}}`

// EventsCommand builds the remote command watching one compose project. The
// project label is what scopes it: `docker events` reports the whole daemon,
// and a busy host may run containers this session has nothing to do with.
//
// No `cd`: the filters are absolute references, and the project name comes
// from what `ps` already reported.
func EventsCommand(project string) string {
	if project == "" {
		return ""
	}
	command := `docker events --format ` + shellQuote(eventFormat) +
		" --filter type=container --filter " +
		shellQuote("label=com.docker.compose.project="+project)
	for _, action := range watchedActions {
		command += " --filter " + shellQuote("event="+action)
	}
	return command
}

// Event is one change the daemon reported.
type Event struct {
	// Action is what happened: `start`, `die`, or a compound the daemon
	// writes with its detail attached, such as `health_status: healthy`.
	Action string
	// Container is the container's name, which is what `ps` reports as Name
	// and what the table's readings are keyed by.
	Container string
	// ExitCode is set on the actions that carry one, `die` above all: the
	// difference between a container that was asked to stop and one that
	// was killed for running out of memory.
	ExitCode string
}

// ParseEvent reads one line of EventsCommand output. A line that is not an
// event — a warning the daemon wrote to stdout, a blank — is reported as not
// ok rather than as an empty event.
func ParseEvent(line string) (Event, bool) {
	fields := strings.Split(strings.TrimRight(line, "\r\n"), eventSeparator)
	if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
		return Event{}, false
	}
	event := Event{Action: strings.TrimSpace(fields[0]), Container: fields[1]}
	if len(fields) > 2 {
		event.ExitCode = strings.TrimSpace(fields[2])
	}
	return event, true
}

// Kind is the action without the detail the daemon appends to it, so
// `health_status: healthy` and `health_status: unhealthy` can be recognized
// as the same kind of thing.
func (e Event) Kind() string {
	kind, _, found := strings.Cut(e.Action, ":")
	if !found {
		return e.Action
	}
	return strings.TrimSpace(kind)
}

// Detail is what the daemon appended, empty for a plain action.
func (e Event) Detail() string {
	_, detail, found := strings.Cut(e.Action, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(detail)
}

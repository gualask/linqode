// Package compose builds the remote `docker compose` commands and parses
// their output into typed models.
package compose

import (
	"fmt"
	"strings"
)

// shellQuote quotes s as a single POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// inDir prefixes command with a `cd` into the compose directory, when one
// is configured (the caller's remote working directory otherwise).
func inDir(composeDir, command string) string {
	if composeDir == "" {
		return command
	}
	return "cd " + shellQuote(composeDir) + " && " + command
}

// PsCommand builds the remote command listing all services of the compose
// project in composeDir (the caller's remote working directory when empty).
//
// `--format json` is NDJSON on compose >= 2.21 and a JSON array before
// that; ParsePS accepts both.
func PsCommand(composeDir string) string {
	return inDir(composeDir, "docker compose ps --all --format json")
}

// InspectRestartsCommand builds the remote command reporting how many times
// docker has restarted each of the given containers: one `name count` line
// per container, empty when there is nothing to ask about.
//
// `compose ps` does not carry restart counts, so they need `docker inspect`.
// The containers `ps` just listed are named here directly rather than
// re-derived with `compose ps -q`: a second compose invocation would cost
// about as much as the first, while a bare `docker inspect` is one cheap
// daemon round-trip. No `cd` either — container names are absolute
// references, not project-relative ones.
func InspectCommand(names []string) string {
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = shellQuote(name)
	}
	return "docker inspect --format '{{.Name}} {{.RestartCount}} {{.State.Pid}}' " +
		strings.Join(quoted, " ")
}

// ServiceAction is a lifecycle action on one compose service.
type ServiceAction int

const (
	ActionRestart ServiceAction = iota
	ActionStop
	ActionStart
)

// Verb is the `docker compose` subcommand this action runs (also the
// natural UI label).
func (a ServiceAction) Verb() string {
	switch a {
	case ActionRestart:
		return "restart"
	case ActionStop:
		return "stop"
	default:
		return "start"
	}
}

// ActionCommand builds the remote command applying action to one service.
func ActionCommand(composeDir string, action ServiceAction, service string) string {
	return inDir(composeDir, "docker compose "+action.Verb()+" "+shellQuote(service))
}

// StatsCommand builds the remote command streaming live resource usage for
// the project's containers, one JSON object per container per sample. This
// is the on-demand mode: docker emits a block per second for as long as the
// command runs.
//
// Three details matter:
//
//   - the ids come from `compose ps -q`, because a bare `docker stats`
//     reports every container on the host, not just this project's;
//   - the whole thing is one brace group, so a failed `cd` aborts it
//     instead of falling through to that host-wide listing;
//   - `exec` replaces the shell with docker stats, so cancelling the stream
//     tears down the process that is actually producing output.
//
// It omits --no-stream: the first sample costs ~2 s of fixed sampling
// latency either way, so a stream pays it once instead of per sample.
func StatsCommand(composeDir string) string {
	stats := `{ ids=$(docker compose ps -q) || exit $?; [ -z "$ids" ] || exec docker stats --format '{{json .}}' $ids; }`
	return inDir(composeDir, stats)
}

// StatsSampleCommand builds the one-shot form of StatsCommand: a single
// block for the project's containers, then exit.
//
// This is what the periodic refresh uses. It costs ~2 s regardless of the
// container count — the daemon reads the cgroups twice, a second apart, to
// derive the CPU percentage — which is why it runs on its own slow interval
// rather than alongside `compose ps` (see docs/PROJECT.md).
func StatsSampleCommand(composeDir string) string {
	stats := `{ ids=$(docker compose ps -q) || exit $?; [ -z "$ids" ] || exec docker stats --no-stream --format '{{json .}}' $ids; }`
	return inDir(composeDir, stats)
}

// LogsCommand builds the remote command reading the logs of one service,
// starting tail lines back and optionally following. `--no-log-prefix` drops
// the service-name prefix (a single service needs none) and `--no-color` its
// ANSI styling; whatever the container itself writes passes through untouched.
func LogsCommand(composeDir, service string, tail int, follow bool) string {
	followFlag := ""
	if follow {
		followFlag = "--follow "
	}
	logs := fmt.Sprintf("docker compose logs %s--no-color --no-log-prefix --tail %d %s",
		followFlag, tail, shellQuote(service))
	return inDir(composeDir, logs)
}

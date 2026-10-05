// Package probe establishes the ground once, at connect time, instead of
// letting the screen discover it through commands that fail.
//
// The rule for what belongs here, and what does not: probe what changes what
// the interface can offer or what error it can explain; guard inline what only
// changes one command's fallback. `timeout` for the mount list and `nvidia-smi`
// for the graphics cards are on the other side of that line and stay there —
// both are one command's fallback, and as probe state they would be machinery
// for nothing.
//
// What passes it is the ground every compose command stands on: whether docker
// is installed at all, whether this user may reach the daemon, which compose
// the host has, and whether the configured project directory exists. A host
// that answers badly to any of these fails every compose command opaquely and
// forever, and the screen never says why.
//
// Nothing here refuses to continue. A probe that finds no docker turns the
// compose capability off and leaves the rest of the session — the meters, the
// process table, the filesystems, the scripts — running, because none of that
// needs a daemon. Capability absence is expressed the way this codebase
// already expresses it: a nil fetch in the backend, which the sampler never
// schedules and the screen never draws an empty panel for.
//
// The mechanism is the host batch's: one round trip, marked sections, a
// missing section degrading to unknown rather than failing the result. The
// cost is one shell fork, one daemon round trip and one compose CLI start,
// paid once per session against the ~65 ms `compose ps` already pays every
// minute (see docs/monitoring.md).
package probe

import (
	"strings"

	"github.com/gualask/linqode/internal/compose"
)

// Section markers, as in the host batch: the parser stays independent of how
// each tool orders or labels what it prints.
const (
	dockerMarker   = "#docker"
	daemonMarker   = "#daemon"
	composeMarker  = "#compose"
	legacyMarker   = "#legacy"
	dirMarker      = "#dir"
	osMarker       = "#os"
	procMarker     = "#proc"
	endpointMarker = "#endpoint"
	contextMarker  = "#context"
	// presentWord is what a shell test echoes when it holds: one word, for
	// the sections that ask a yes-or-no question.
	presentWord = "present"
)

// markers is every section the batch prints, and the only lines split reads
// as the start of one.
var markers = map[string]bool{
	dockerMarker: true, daemonMarker: true, composeMarker: true,
	legacyMarker: true, dirMarker: true, osMarker: true, procMarker: true,
	endpointMarker: true, contextMarker: true,
}

// Docker is what the daemon answered, which is not the same question as
// whether docker is installed.
//
// The distinction is the whole reason this exists. A host where the daemon
// runs but the operator is not in the `docker` group reports a docker binary,
// a compose plugin, and a refusal — and a probe that only asked "did `docker
// ps` print anything" would call that a host without docker and be wrong in
// the one way that matters, because the fix is one `usermod` away and the
// operator cannot know that from an empty table.
type Docker int

const (
	// DockerUnknown is a host that did not answer this section at all.
	DockerUnknown Docker = iota
	// DockerAbsent is a host with no docker binary on PATH.
	DockerAbsent
	// DockerDenied is a reachable socket this user may not open.
	DockerDenied
	// DockerUnreachable is a docker binary whose daemon did not answer for
	// some other reason — not running, a bad DOCKER_HOST, a broken socket.
	// Unlike the two above it is not a finding: a daemon that was restarting
	// at the moment of connecting answers a minute later, and turning compose
	// off for the session on its account would outlast the outage. It is
	// treated like a daemon that timed out — carry on, and let the refresh
	// report what docker says — with DaemonMessage kept for whoever asks.
	DockerUnreachable
	// DockerReady is a daemon that answered with its version.
	DockerReady
)

// Compose is which compose the host has. Only the `docker compose` plugin is
// driven. The standalone `docker-compose` binary is detected so the screen can
// say so, not adapted to: adapting would put the binary's name in front of
// every compose command this codebase builds, which is a lot of indirection to
// carry for what is, in its v1 form, a tool its own authors stopped shipping
// in June 2023.
//
// The standalone binary is not always v1, though. Compose v2 ships as one too,
// and some distributions and manual installs put it on PATH without the
// plugin. That host needs the plugin installed or linked, not an upgrade, so
// the version the binary reports is what the sentence turns on.
type Compose int

const (
	ComposeUnknown Compose = iota
	// ComposeAbsent is a host with neither the plugin nor the old binary.
	ComposeAbsent
	// ComposeLegacy is the standalone `docker-compose` binary and no plugin.
	// ComposeVersion says which one, when the binary said.
	ComposeLegacy
	// ComposeV2 is the `docker compose` plugin.
	ComposeV2
)

// Directory is what became of the configured `compose_dir`. Unconfigured is
// not a failure: the commands then run in the login directory, which is a
// supported way to use this.
type Directory int

const (
	DirectoryUnknown Directory = iota
	// DirectoryUnconfigured is a host with no compose_dir in the config.
	DirectoryUnconfigured
	// DirectoryMissing is a configured path that is not a directory on the
	// host — a typo, or a project that moved.
	DirectoryMissing
	// DirectoryPresent is a configured path that is there.
	DirectoryPresent
)

// Proc is whether the host has the /proc filesystem the machine readings are
// made of. It is not a Linux-or-not question dressed up: what matters is that
// the process table, the load average, the memory and the CPU counters are
// all reads of files under /proc, and a host without it answers the batch
// with eight empty sections and a real `df`.
//
// The screen already declines to draw a reading it did not get. What this
// adds is the one capability that is *all* /proc — the process table — which
// would otherwise be an openable panel that can only ever be empty.
type Proc int

const (
	// ProcUnknown is a host that did not answer this section.
	ProcUnknown Proc = iota
	// ProcAbsent is a host with no readable /proc: a Mac, mainly.
	ProcAbsent
	// ProcPresent is a host whose /proc/stat can be read.
	ProcPresent
)

// Result is what one probe established. Every field has an Unknown state and
// every consumer must treat it as "carry on": a host that answered nothing is
// a host this refuses to make claims about, not a host to shut features off on.
type Result struct {
	Docker Docker
	// DaemonVersion is the server version when the daemon answered.
	DaemonVersion string
	// DaemonMessage is what the daemon said instead, when it did not. It is
	// the docker CLI's own sentence, kept verbatim, because it names the
	// socket it tried and is more useful than anything paraphrasing it.
	DaemonMessage string

	Compose Compose
	// ComposeVersion is what `compose version --short` reported, when it did:
	// the plugin's, or the standalone binary's when there is no plugin.
	ComposeVersion string

	Directory Directory
	// DirectoryPath is the configured path this result is about.
	DirectoryPath string

	// OS is the host's PRETTY_NAME from /etc/os-release. Cosmetic: it is here
	// because it is free once the batch exists, not because anything turns on
	// it.
	OS string

	Proc Proc

	// DockerHost is DOCKER_HOST as the session's environment has it, and
	// DockerContext the context docker will use: what `docker context show`
	// says, which is DOCKER_CONTEXT when that is exported and otherwise the
	// one `docker context use` saved in the CLI's config — the common way to
	// pick one, and one an environment variable alone would never see. Where
	// the CLI is too old to answer, it is DOCKER_CONTEXT again.
	//
	// They are read because a local session inherits the operator's
	// environment: with DOCKER_HOST=ssh://prod exported, a session whose
	// header says `local` is driving production, and nothing on the screen
	// would say so. Over SSH the same variables are almost always unset,
	// sshd's environment being minimal — but a host that does set one is a
	// host where the same sentence is worth showing, and asking costs one
	// echo either way. There is no second command shape for a second
	// transport.
	DockerHost, DockerContext string
}

// DockerEndpoint is the daemon this session will reach when it is not the
// local socket, and empty when it is. DOCKER_HOST wins: docker reads it
// before it reads the context. A DOCKER_HOST spelling out the socket docker
// would have used anyway is the local socket, and saying it would put a
// warning on the one session that has nothing to warn about.
func (r Result) DockerEndpoint() string {
	if r.DockerHost != "" {
		if r.DockerHost == defaultSocket {
			return ""
		}
		return r.DockerHost
	}
	if r.DockerContext != "" && r.DockerContext != "default" {
		return "context " + r.DockerContext
	}
	return ""
}

// defaultSocket is where docker looks when nothing says otherwise.
const defaultSocket = "unix:///var/run/docker.sock"

// CanReadProc reports whether the readings made of /proc are worth asking
// for. Unknown counts as yes, as everywhere else here: a probe that
// established nothing must not be the reason a working host loses its
// process table.
func (r Result) CanReadProc() bool { return r.Proc != ProcAbsent }

// ComposeUnavailable is why compose commands cannot work on this host, and
// the empty string when they can. It is one sentence because it goes where the
// table would have been, and an operator reading it is being told why a panel
// is empty, not being given a report.
//
// Every case here is permanent for the life of the session, which is what
// separates them from a refresh that failed: none of them will come right on
// the next interval, and saying so once is the whole point of probing. What is
// deliberately not here is anything transient — a daemon that answers now and
// times out later, or one that was not running at the moment of connecting,
// is the refresh error's business, not this.
func (r Result) ComposeUnavailable() string {
	switch r.Docker {
	case DockerAbsent:
		return "docker is not installed on this host"
	case DockerDenied:
		// The docker group is named because it is the fix, and because the
		// operator cannot deduce it from an empty table. It is a guess about
		// the cause, so it is phrased as one.
		return "the docker daemon refuses this user — not in the `docker` group?"
	}
	// DockerUnreachable is deliberately not a case: see its comment.
	switch r.Compose {
	case ComposeLegacy:
		return standaloneUnavailable(r.ComposeVersion)
	case ComposeAbsent:
		return "docker is here, but the compose plugin is not"
	}
	if r.Directory == DirectoryMissing {
		return "compose_dir " + r.DirectoryPath + " is not a directory on this host"
	}
	return ""
}

// standaloneUnavailable is the sentence for a host whose only compose is the
// standalone binary. v1 is named as such because there is nothing to link, and
// a v2 binary is told apart because there is.
func standaloneUnavailable(version string) string {
	switch {
	case version == "":
		return "this host has only the standalone docker-compose, which linqode does not drive"
	case strings.HasPrefix(strings.TrimPrefix(version, "v"), "1."):
		return "this host has docker-compose v1 (" + version + "), which linqode does not drive"
	default:
		return "this host has the standalone docker-compose " + version +
			", but not the compose plugin linqode drives"
	}
}

// CanCompose reports whether compose commands are worth offering at all.
//
// Unknown counts as yes, deliberately, and it is why this asks
// ComposeUnavailable rather than testing the fields itself: a probe that
// failed to establish the ground must not be the reason a working host loses
// its table. The commands are still there to fail with their own message,
// which is the situation this package improves on and never the situation it
// should create.
func (r Result) CanCompose() bool { return r.ComposeUnavailable() == "" }

// daemonCommand asks the daemon for its version, and is the one question in
// the batch that waits on something other than the shell: every other section
// reads a file or starts a CLI that answers on its own. A daemon that accepts
// the connection and never replies, or a DOCKER_HOST=ssh:// whose connection
// stalls, would hold the batch — and the batch runs before the screen does, so
// the session would sit at "Connecting…" for good.
//
// Five seconds is the mount list's bound, for the same reason. A daemon cut
// off there prints nothing, which leaves the section empty and the daemon
// Unknown: carry on, and let the refresh report what it finds. That is not a
// permanent finding, and a probe must not turn compose off on a daemon that
// was only slow once. `timeout` is not POSIX, so a host without it asks
// unguarded, which is what every host did before.
//
// Whether it has one is asked by running it rather than with `command -v`:
// BusyBox before 1.30 has a `timeout` that wants `-t 5`, reads `timeout 5
// docker` as a program named `5`, and would put its error where the daemon's
// answer goes. `timeout 1 true` fails the same way there. The same guard is
// internal/host's, for the mount list.
const daemonCommand = "if timeout 1 true >/dev/null 2>&1; " +
	"then timeout 5 docker version --format '{{.Server.Version}}' 2>&1; " +
	"else docker version --format '{{.Server.Version}}' 2>&1; fi"

// Command is the remote command establishing the ground, in one round trip.
//
// Each section prints evidence rather than a verdict; the verdict is Parse's,
// on the client. That is the same division the rest of this codebase keeps —
// drive standard CLIs remotely, interpret their output locally — and it is
// what lets a host answering something unforeseen be classified again later
// without a second round trip to a server that has to be reasoned about.
//
// The daemon section redirects stderr into the output on purpose: when docker
// cannot reach its socket, everything worth reading is on stderr, and the
// message names the socket it tried.
//
// `docker compose version` needs the CLI but not the daemon, so a host whose
// socket refuses this user still reports which compose it has. That is what
// makes the difference between "no docker here" and "docker is here and will
// not talk to you" reportable in one sentence.
//
// The standalone binary is asked for its version only when the plugin gave
// none. A host with both is a host with the plugin, and v1 is a Python program
// that takes most of a second to start — a cost the ordinary host must not pay
// to answer a question it has no use for. A binary that will not say its
// version still prints a word, so the section tells "there is one" apart from
// "there is none".
func Command(composeDir string) string {
	var b strings.Builder
	b.WriteString("echo '" + dockerMarker + "'; command -v docker 2>/dev/null; ")
	b.WriteString("echo '" + daemonMarker + "'; " +
		"if command -v docker >/dev/null 2>&1; then " + daemonCommand + "; fi; ")
	b.WriteString("echo '" + composeMarker + "'; compose_plugin=; " +
		"if command -v docker >/dev/null 2>&1; then " +
		`compose_plugin=$(docker compose version --short 2>/dev/null); echo "$compose_plugin"; fi; `)
	b.WriteString("echo '" + legacyMarker + "'; " +
		`if [ -z "$compose_plugin" ] && command -v docker-compose >/dev/null 2>&1; then ` +
		"docker-compose version --short 2>/dev/null || echo " + presentWord + "; fi; ")
	b.WriteString("echo '" + dirMarker + "'; ")
	if composeDir != "" {
		// compose's quoting rather than a copy of it: the directory tested
		// here must be the one its `cd` enters, a leading `~/` included.
		b.WriteString("[ -d " + compose.QuoteDir(composeDir) + " ] && echo " + presentWord + "; ")
	}
	b.WriteString("echo '" + osMarker + "'; " +
		"grep '^PRETTY_NAME=' /etc/os-release 2>/dev/null; ")
	b.WriteString("echo '" + procMarker + "'; " +
		"[ -r /proc/stat ] && echo " + presentWord + "; ")
	// Quoted, because an unset variable must print an empty section rather
	// than nothing at all — the difference between "no endpoint set" and "the
	// host never got this far".
	b.WriteString("echo '" + endpointMarker + `'; echo "$DOCKER_HOST"; `)
	// The context is docker's to resolve, not the environment's: `docker
	// context use` saves it in the CLI's config, where no variable shows it.
	// It reads that config and asks no daemon, so it needs no bound.
	b.WriteString("echo '" + contextMarker + "'; " +
		"if command -v docker >/dev/null 2>&1; then " +
		`docker context show 2>/dev/null || echo "$DOCKER_CONTEXT"; ` +
		`else echo "$DOCKER_CONTEXT"; fi`)
	return b.String()
}

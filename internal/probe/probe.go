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
	dirPresentWord = "present"
)

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
	DockerUnreachable
	// DockerReady is a daemon that answered with its version.
	DockerReady
)

// Compose is which compose the host has. v1 is the standalone `docker-compose`
// binary, end of life since June 2023; it is detected so the screen can say so,
// not adapted to. Adapting would put the binary's name in front of every
// compose command this codebase builds, which is a lot of indirection to carry
// for a tool its own authors stopped shipping.
type Compose int

const (
	ComposeUnknown Compose = iota
	// ComposeAbsent is a host with neither the plugin nor the old binary.
	ComposeAbsent
	// ComposeLegacy is the standalone v1 binary and no plugin.
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
	// ComposeVersion is what `compose version --short` reported, when it did.
	ComposeVersion string

	Directory Directory
	// DirectoryPath is the configured path this result is about.
	DirectoryPath string

	// OS is the host's PRETTY_NAME from /etc/os-release. Cosmetic: it is here
	// because it is free once the batch exists, not because anything turns on
	// it.
	OS string
}

// ComposeUnavailable is why compose commands cannot work on this host, and
// the empty string when they can. It is one sentence because it goes where the
// table would have been, and an operator reading it is being told why a panel
// is empty, not being given a report.
//
// Every case here is permanent for the life of the session, which is what
// separates them from a refresh that failed: none of them will come right on
// the next interval, and saying so once is the whole point of probing. What is
// deliberately not here is anything transient — a daemon that answers now and
// times out later is the refresh error's business, not this.
func (r Result) ComposeUnavailable() string {
	switch r.Docker {
	case DockerAbsent:
		return "docker is not installed on this host"
	case DockerDenied:
		// The docker group is named because it is the fix, and because the
		// operator cannot deduce it from an empty table. It is a guess about
		// the cause, so it is phrased as one.
		return "the docker daemon refuses this user — not in the `docker` group?"
	case DockerUnreachable:
		if r.DaemonMessage != "" {
			return "the docker daemon did not answer: " + r.DaemonMessage
		}
		return "the docker daemon did not answer"
	}
	switch r.Compose {
	case ComposeLegacy:
		return "this host has docker-compose v1, which linqode does not drive"
	case ComposeAbsent:
		return "docker is here, but the compose plugin is not"
	}
	if r.Directory == DirectoryMissing {
		return "compose_dir " + r.DirectoryPath + " is not a directory on this host"
	}
	return ""
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

// CanReachDaemon reports whether commands that talk to docker but not through
// compose are worth offering — `docker system df`, and the event stream.
//
// It is a separate question on purpose. A host with a working daemon and
// compose v1 has no service table and still has a daemon holding images,
// containers, volumes and build cache, which is a reading worth showing to
// somebody who cannot get at the project any other way. Unknown counts as yes
// here for the same reason it does above.
func (r Result) CanReachDaemon() bool {
	return r.Docker != DockerAbsent && r.Docker != DockerDenied &&
		r.Docker != DockerUnreachable
}

// shellQuote quotes s as a single POSIX shell word. The same three lines live
// in internal/compose; sharing them would mean a package dependency in one
// direction or the other purely to pass a string through, which is a worse
// trade than the duplication.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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
func Command(composeDir string) string {
	var b strings.Builder
	b.WriteString("echo '" + dockerMarker + "'; command -v docker 2>/dev/null; ")
	b.WriteString("echo '" + daemonMarker + "'; " +
		"if command -v docker >/dev/null 2>&1; then " +
		"docker version --format '{{.Server.Version}}' 2>&1; fi; ")
	b.WriteString("echo '" + composeMarker + "'; " +
		"if command -v docker >/dev/null 2>&1; then " +
		"docker compose version --short 2>/dev/null; fi; ")
	b.WriteString("echo '" + legacyMarker + "'; command -v docker-compose 2>/dev/null; ")
	b.WriteString("echo '" + dirMarker + "'; ")
	if composeDir != "" {
		b.WriteString("[ -d " + shellQuote(composeDir) + " ] && echo " + dirPresentWord + "; ")
	}
	b.WriteString("echo '" + osMarker + "'; " +
		"grep '^PRETTY_NAME=' /etc/os-release 2>/dev/null")
	return b.String()
}

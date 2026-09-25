package probe

import (
	"strings"
	"testing"
)

// The healthy host the fixture is: a daemon that answers, the compose plugin,
// and the project directory where the config says it is.
func TestAWorkingHostTurnsNothingOff(t *testing.T) {
	result := Parse([]byte(`#docker
/usr/bin/docker
#daemon
29.7.0
#compose
5.3.1
#legacy
#dir
present
#os
PRETTY_NAME="Alpine Linux v3.22"
`), "/srv/app")

	if result.Docker != DockerReady {
		t.Errorf("Docker = %v, want DockerReady", result.Docker)
	}
	if result.DaemonVersion != "29.7.0" {
		t.Errorf("DaemonVersion = %q", result.DaemonVersion)
	}
	if result.Compose != ComposeV2 || result.ComposeVersion != "5.3.1" {
		t.Errorf("Compose = %v %q, want ComposeV2 5.3.1", result.Compose, result.ComposeVersion)
	}
	if result.Directory != DirectoryPresent {
		t.Errorf("Directory = %v, want DirectoryPresent", result.Directory)
	}
	if result.OS != "Alpine Linux v3.22" {
		t.Errorf("OS = %q, quotes not stripped?", result.OS)
	}
	if why := result.ComposeUnavailable(); why != "" {
		t.Errorf("a working host was told %q", why)
	}
}

// The case the probe exists for, and the one omnyssh gets wrong: the daemon is
// running, the plugin is installed, and this user may not open the socket. It
// must not be reported as a host without docker.
func TestADeniedSocketIsNotAHostWithoutDocker(t *testing.T) {
	result := Parse([]byte(`#docker
/usr/bin/docker
#daemon
permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get "http://%2Fvar%2Frun%2Fdocker.sock/v1.51/version": dial unix /var/run/docker.sock: connect: permission denied
#compose
5.3.1
#legacy
#dir
present
#os
`), "/srv/app")

	if result.Docker != DockerDenied {
		t.Errorf("Docker = %v, want DockerDenied", result.Docker)
	}
	// The plugin answers without the daemon, which is what makes the
	// difference between "no docker here" and "docker will not talk to you"
	// reportable at all.
	if result.Compose != ComposeV2 {
		t.Errorf("Compose = %v, want ComposeV2 — the plugin does not need the daemon",
			result.Compose)
	}
	why := result.ComposeUnavailable()
	if !strings.Contains(why, "docker") || !strings.Contains(why, "group") {
		t.Errorf("ComposeUnavailable() = %q, which does not name the fix", why)
	}
}

func TestNoDockerAtAll(t *testing.T) {
	result := Parse([]byte("#docker\n#daemon\n#compose\n#legacy\n#dir\n#os\n"), "")
	if result.Docker != DockerAbsent {
		t.Errorf("Docker = %v, want DockerAbsent", result.Docker)
	}
	if result.CanCompose() {
		t.Error("a host with no docker was offered compose commands")
	}
	if result.Directory != DirectoryUnconfigured {
		t.Errorf("Directory = %v, want DirectoryUnconfigured", result.Directory)
	}
}

func TestADaemonThatIsNotRunning(t *testing.T) {
	result := Parse([]byte(`#docker
/usr/bin/docker
#daemon
Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?
#compose
5.3.1
#legacy
#dir
#os
`), "")
	if result.Docker != DockerUnreachable {
		t.Errorf("Docker = %v, want DockerUnreachable", result.Docker)
	}
	// The daemon's own sentence names the socket it tried, which is more
	// useful than anything paraphrasing it.
	if !strings.Contains(result.ComposeUnavailable(), "unix:///var/run/docker.sock") {
		t.Errorf("the daemon's own message was dropped: %q", result.ComposeUnavailable())
	}
}

// A warning on stderr precedes the version, because the daemon section
// captures stderr on purpose. The version still wins: the daemon answered.
func TestAWarningBeforeTheVersionIsStillAnAnsweringDaemon(t *testing.T) {
	result := Parse([]byte(`#docker
/usr/bin/docker
#daemon
WARNING: daemon is using an insecure registry
27.1.2
#compose
5.3.1
`), "")
	if result.Docker != DockerReady || result.DaemonVersion != "27.1.2" {
		t.Errorf("Docker = %v %q, want DockerReady 27.1.2", result.Docker, result.DaemonVersion)
	}
}

// The daemon section captures stderr, so the refusal is not always the first
// thing in it: the CLI can warn about its own config first.
func TestADenialAfterAWarningIsStillADenial(t *testing.T) {
	result := Parse([]byte(`#docker
/usr/bin/docker
#daemon
WARNING: Error loading config file: /home/deploy/.docker/config.json: is a directory
permission denied while trying to connect to the docker API at unix:///var/run/docker.sock
#compose
5.3.1
`), "")
	if result.Docker != DockerDenied {
		t.Errorf("Docker = %v, want DockerDenied", result.Docker)
	}
	if !strings.Contains(result.DaemonMessage, "docker API") {
		t.Errorf("DaemonMessage = %q, want the denial rather than the warning", result.DaemonMessage)
	}
}

// Only a refused socket is the `docker` group's business. A denial about
// anything else sends the operator to the wrong fix.
func TestADenialThatIsNotTheSocketIsNotAGroupProblem(t *testing.T) {
	for name, section := range map[string]string{
		"ssh endpoint refusing a key": "error during connect: Get \"http://docker.example.com/v1.51/version\": " +
			"command [ssh -o ConnectTimeout=30 -T -- deploy@prod docker system dial-stdio] has exited " +
			"with exit status 255, make sure the URL is valid, and Docker 18.09 or later is installed " +
			"on the remote host: stderr=deploy@prod: Permission denied (publickey).\n",
		"unreadable config, daemon down": "WARNING: Error loading config file: " +
			"/home/deploy/.docker/config.json: open /home/deploy/.docker/config.json: permission denied\n" +
			"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n",
	} {
		t.Run(name, func(t *testing.T) {
			result := Parse([]byte("#docker\n/usr/bin/docker\n#daemon\n"+section+"#compose\n5.3.1\n"), "")
			if result.Docker != DockerUnreachable {
				t.Errorf("Docker = %v, want DockerUnreachable", result.Docker)
			}
			if strings.Contains(result.ComposeUnavailable(), "group") {
				t.Errorf("ComposeUnavailable() = %q names the docker group", result.ComposeUnavailable())
			}
			if isWarning(result.DaemonMessage) {
				t.Errorf("DaemonMessage = %q, a warning rather than the diagnosis", result.DaemonMessage)
			}
		})
	}
}

// v1 is detected so the screen can name it, not adapted to.
func TestComposeV1IsNamedRatherThanDriven(t *testing.T) {
	result := Parse([]byte(`#docker
/usr/bin/docker
#daemon
20.10.24
#compose
#legacy
/usr/local/bin/docker-compose
#dir
#os
`), "")
	if result.Compose != ComposeLegacy {
		t.Errorf("Compose = %v, want ComposeLegacy", result.Compose)
	}
	if !strings.Contains(result.ComposeUnavailable(), "docker-compose v1") {
		t.Errorf("ComposeUnavailable() = %q, which does not name the version",
			result.ComposeUnavailable())
	}
}

func TestDockerWithoutThePlugin(t *testing.T) {
	result := Parse([]byte("#docker\n/usr/bin/docker\n#daemon\n29.7.0\n#compose\n#legacy\n"), "")
	if result.Compose != ComposeAbsent {
		t.Errorf("Compose = %v, want ComposeAbsent", result.Compose)
	}
	if result.CanCompose() {
		t.Error("a host without the plugin was offered compose commands")
	}
}

// A compose_dir that is not there fails every compose command on the `cd`, for
// the life of the session. It belongs with the others: said once, not
// discovered again on every safety-net interval.
func TestAMissingComposeDirIsPermanent(t *testing.T) {
	result := Parse([]byte("#docker\n/usr/bin/docker\n#daemon\n29.7.0\n#compose\n5.3.1\n#legacy\n#dir\n"),
		"/srv/gone")
	if result.Directory != DirectoryMissing {
		t.Errorf("Directory = %v, want DirectoryMissing", result.Directory)
	}
	if !strings.Contains(result.ComposeUnavailable(), "/srv/gone") {
		t.Errorf("ComposeUnavailable() = %q, which does not name the path",
			result.ComposeUnavailable())
	}
}

// The rule the whole package turns on: what was not established is not a
// finding. A probe that answered nothing must never be the reason a working
// host loses its table.
func TestNothingEstablishedTurnsNothingOff(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty": nil,
		// What a daemon cut off by the timeout leaves behind.
		"timed out": []byte("#docker\n/usr/bin/docker\n#daemon\n#compose\n5.3.1\n#legacy\n#dir\npresent\n"),
		"garbage":   []byte("bash: line 1: syntax error\n"),
		"truncated": []byte("#docker\n/usr/bin/docker\n#daemon\n29.7.0\n"),
	} {
		result := Parse(raw, "/srv/app")
		if !result.CanCompose() {
			t.Errorf("%s: compose was turned off by %q", name, result.ComposeUnavailable())
		}
	}
	if result := Parse(nil, ""); result.Docker != DockerUnknown ||
		result.Compose != ComposeUnknown || result.Directory != DirectoryUnknown {
		t.Errorf("an empty answer claimed something: %+v", result)
	}
}

func TestCommandAsksForEverySection(t *testing.T) {
	command := Command("/srv/app")
	for _, marker := range []string{
		dockerMarker, daemonMarker, composeMarker, legacyMarker, dirMarker, osMarker,
		procMarker, endpointMarker, contextMarker,
	} {
		if !strings.Contains(command, marker) {
			t.Errorf("Command() does not emit marker %q", marker)
		}
	}
	// Everything worth reading about a refused socket is on stderr.
	if !strings.Contains(command, "'{{.Server.Version}}' 2>&1") {
		t.Errorf("the daemon's error would be lost: %s", command)
	}
	// The daemon is the one section that can wait forever, and the batch
	// runs before the screen does.
	if !strings.Contains(command, "timeout 5 docker version") {
		t.Errorf("the daemon is asked without a bound: %s", command)
	}
	// A host without docker must not have the shell report a missing binary
	// on stderr for each of the two commands that would have used it.
	if strings.Count(command, "command -v docker >/dev/null 2>&1") != 2 {
		t.Errorf("docker is invoked unguarded: %s", command)
	}
	// A path with a quote in it is one shell word, not an injection.
	if !strings.Contains(Command(`/srv/it's`), `[ -d '/srv/it'\''s' ]`) {
		t.Errorf("the compose dir is not quoted: %s", Command(`/srv/it's`))
	}
}

// With no compose_dir configured there is no directory to test, and the batch
// must not ask about one — `[ -d ” ]` would report a missing directory that
// was never configured.
func TestNoComposeDirIsNotAMissingOne(t *testing.T) {
	if strings.Contains(Command(""), "[ -d ") {
		t.Errorf("an unconfigured compose dir was tested for: %s", Command(""))
	}
	// And with nothing configured, whatever the section holds is not about a
	// directory this session has an opinion on.
	if got := Parse([]byte("#dir\npresent\n"), "").Directory; got != DirectoryUnconfigured {
		t.Errorf("Directory = %v, want DirectoryUnconfigured", got)
	}
}

// A machine with no /proc answers eight of the batch's ten sections with
// nothing, and the process table — which is /proc and nothing else — is the
// one capability that becomes a panel that can only ever be empty.
func TestAHostWithoutProcKeepsEverythingItStillHas(t *testing.T) {
	result := Parse([]byte("#docker\n/opt/homebrew/bin/docker\n#daemon\n29.7.0\n"+
		"#compose\n5.5.1\n#legacy\n#dir\npresent\n#os\n#proc\n#endpoint\n#context\n"), "/srv/app")

	if result.Proc != ProcAbsent {
		t.Errorf("Proc = %v, want ProcAbsent", result.Proc)
	}
	if result.CanReadProc() {
		t.Error("CanReadProc on a host with no /proc")
	}
	// Everything else this host does have is untouched: the readings that
	// are not /proc, and compose above all.
	if !result.CanCompose() {
		t.Errorf("a Mac with a working daemon was told %q", result.ComposeUnavailable())
	}
}

func TestProcPresentAndUnknownBothCarryOn(t *testing.T) {
	present := Parse([]byte("#proc\npresent\n"), "")
	if present.Proc != ProcPresent || !present.CanReadProc() {
		t.Errorf("Proc = %v", present.Proc)
	}
	// A batch that never reached the section establishes nothing, and
	// nothing established turns nothing off.
	silent := Parse([]byte("#docker\n/usr/bin/docker\n"), "")
	if silent.Proc != ProcUnknown || !silent.CanReadProc() {
		t.Errorf("Proc = %v, want ProcUnknown carrying on", silent.Proc)
	}
}

// A local session inherits the operator's environment, so the header can say
// `local` while the daemon is somewhere else entirely.
func TestTheDockerEndpointIsReportedWhenItIsNotTheSocket(t *testing.T) {
	for _, tc := range []struct {
		name    string
		section string
		want    string
	}{
		{"unset", "#endpoint\n\n#context\n\n", ""},
		{"DOCKER_HOST", "#endpoint\nssh://deploy@prod\n#context\n\n", "ssh://deploy@prod"},
		{"context", "#endpoint\n\n#context\ncolima\n", "context colima"},
		{"the default context is the socket", "#endpoint\n\n#context\ndefault\n", ""},
		// docker reads DOCKER_HOST before it reads the context, and so does
		// this: a session with both set reaches the one docker will.
		{"both", "#endpoint\ntcp://10.0.0.2:2375\n#context\ncolima\n", "tcp://10.0.0.2:2375"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse([]byte(tc.section), "").DockerEndpoint(); got != tc.want {
				t.Errorf("DockerEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

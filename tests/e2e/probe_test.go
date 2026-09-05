//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/gualask/linqode/internal/probe"
)

// The probe against a real daemon. The fixture is the case it must not get
// wrong in the ordinary direction: an ordinary account in the `docker` group,
// the compose plugin, and a project directory that is there — a host where
// every finding must be "nothing to report", because a probe that turns
// capabilities off on a working host is worse than no probe at all.
func TestProbeAgainstRealHost(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	out, err := session.Exec(ctx, probe.Command(composeDir))
	if err != nil {
		t.Fatalf("probing the host: %v", err)
	}
	result := probe.Parse(out.Stdout, composeDir)

	if why := result.ComposeUnavailable(); why != "" {
		t.Fatalf("a working host was refused compose: %s\noutput was:\n%s", why, out.Stdout)
	}
	if result.Docker != probe.DockerReady {
		t.Errorf("Docker = %v, want DockerReady\noutput was:\n%s", result.Docker, out.Stdout)
	}
	// The daemon answered its own version, which is the thing a probe keyed
	// on "did the command print anything" cannot tell from an error.
	if result.DaemonVersion == "" || !strings.ContainsRune(result.DaemonVersion, '.') {
		t.Errorf("DaemonVersion = %q", result.DaemonVersion)
	}
	if result.Compose != probe.ComposeV2 || result.ComposeVersion == "" {
		t.Errorf("Compose = %v %q, want ComposeV2 with a version",
			result.Compose, result.ComposeVersion)
	}
	if result.Directory != probe.DirectoryPresent {
		t.Errorf("Directory = %v for %s, which the fixture created",
			result.Directory, composeDir)
	}
	// Alpine, in this fixture. What matters is that something was read, not
	// which distribution it is.
	if result.OS == "" {
		t.Errorf("no PRETTY_NAME read\noutput was:\n%s", out.Stdout)
	}

	// Nothing on stderr, on a host that has every one of these. The batch
	// guards each command that might not be there, and a guard that leaked
	// would put "not found" in front of an operator for a reading that
	// succeeded.
	if len(out.Stderr) != 0 {
		t.Errorf("the probe wrote to stderr: %s", out.Stderr)
	}
}

// A compose_dir that is not there, against the real host. This is the one
// finding the fixture can produce on demand, and the only one of the four that
// does not need a differently built machine.
func TestProbeFindsAMissingComposeDir(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	const gone = "/srv/there-is-no-project-here"
	out, err := session.Exec(ctx, probe.Command(gone))
	if err != nil {
		t.Fatalf("probing the host: %v", err)
	}
	result := probe.Parse(out.Stdout, gone)

	if result.Directory != probe.DirectoryMissing {
		t.Errorf("Directory = %v for a path that is not there", result.Directory)
	}
	if result.CanCompose() {
		t.Error("compose was offered for a project directory that does not exist")
	}
	// The daemon is fine; only the path is wrong, and the sentence must say
	// which of the two it is.
	if result.Docker != probe.DockerReady {
		t.Errorf("Docker = %v — a missing directory was read as a broken daemon",
			result.Docker)
	}
	if !strings.Contains(result.ComposeUnavailable(), gone) {
		t.Errorf("ComposeUnavailable() = %q, which does not name the path",
			result.ComposeUnavailable())
	}
}

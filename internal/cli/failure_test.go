package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/probe"
	"github.com/gualask/linqode/internal/remote"
)

func TestNonInteractivePrompterReturnsActionableTypedErrors(t *testing.T) {
	prompter := NonInteractivePrompter{}
	accepted, err := prompter.ConfirmHostKey("example.com", 2222, "ssh-ed25519", "SHA256:abc")
	if accepted {
		t.Fatal("unknown host key unexpectedly accepted")
	}
	var unknown *remote.UnknownHostKeyError
	if !errors.As(err, &unknown) || unknown.Host != "example.com" || unknown.Port != 2222 ||
		unknown.Algorithm != "ssh-ed25519" || unknown.Fingerprint != "SHA256:abc" {
		t.Fatalf("host-key error = %#v, %v", unknown, err)
	}

	passphrase, err := prompter.AskPassphrase("/keys/deploy")
	if passphrase != "" {
		t.Fatalf("passphrase = %q", passphrase)
	}
	var required *remote.PassphraseRequiredError
	if !errors.As(err, &required) || required.Path != "/keys/deploy" {
		t.Fatalf("passphrase error = %#v, %v", required, err)
	}
}

func TestConnectionFailureKinds(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind string
	}{
		{name: "unknown key", err: &remote.UnknownHostKeyError{}, kind: "unknown_host_key"},
		{name: "changed key", err: &remote.HostKeyChangedError{}, kind: "changed_host_key"},
		{name: "passphrase", err: &remote.PassphraseRequiredError{}, kind: "passphrase_required"},
		{name: "auth", err: &remote.AuthFailedError{}, kind: "authentication_failed"},
		{name: "transport", err: errors.New("dial failed"), kind: "connection_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := ConnectionFailure("status", test.err)
			if failure.Kind != test.kind || failure.ExitCode != 1 || failure.Operation != "status" {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
	if failure := ConnectionFailure("status", context.Canceled); failure.ExitCode != 130 {
		t.Fatalf("cancelled connection failure = %+v", failure)
	}
}

func TestReadFailureKinds(t *testing.T) {
	tests := []struct {
		err      error
		kind     string
		exitCode int
	}{
		{err: operations.UnknownServiceError{Name: "web"}, kind: "unknown_service", exitCode: 2},
		{err: operations.ParseError{Err: errors.New("bad data")}, kind: "parse_failed", exitCode: 1},
		{err: operations.RemoteCommandError{ExitCode: -1, Message: "missing"}, kind: "remote_exit_missing", exitCode: 1},
		{err: errors.New("connection lost"), kind: "transport_failed", exitCode: 1},
	}
	for _, test := range tests {
		failure := ReadFailure("logs", test.err)
		if failure.Kind != test.kind || failure.ExitCode != test.exitCode {
			t.Fatalf("ReadFailure(%T) = %+v", test.err, failure)
		}
	}
}

func TestMutationFailureKinds(t *testing.T) {
	tests := []struct {
		err      error
		kind     string
		exitCode int
	}{
		{err: operations.UnknownServiceError{Name: "web"}, kind: "unknown_service", exitCode: 2},
		{err: operations.UnknownScriptError{Name: "deploy"}, kind: "unknown_script", exitCode: 2},
		{err: operations.InvalidActionError{Action: 99}, kind: "invalid_argument", exitCode: 2},
		{err: operations.RemoteCommandError{ExitCode: -1, Message: "missing"}, kind: "remote_exit_missing", exitCode: 1},
		{err: errors.New("connection lost"), kind: "transport_failed", exitCode: 1},
	}
	for _, test := range tests {
		failure := MutationFailure("script", test.err)
		if failure.Kind != test.kind || failure.ExitCode != test.exitCode {
			t.Fatalf("MutationFailure(%T) = %+v", test.err, failure)
		}
	}
}

func TestReadPreflightRejectsDisabledStatsOnly(t *testing.T) {
	if failure := ReadPreflight(Invocation{Command: CommandStats, Host: "production"}, false); failure == nil ||
		failure.Kind != "unsupported_operation" || failure.ExitCode != 1 {
		t.Fatalf("stats failure = %+v", failure)
	}
	if failure := ReadPreflight(Invocation{Command: CommandStatus, Host: "production"}, false); failure != nil {
		t.Fatalf("status preflight = %+v", failure)
	}
}

// The three conditions a caller would do different things about: a host to
// install docker on, an account to add to a group, and a path to correct in
// the config. They were one opaque remote_command_failed before this.
func TestComposePreflightNamesTheCondition(t *testing.T) {
	tests := []struct {
		name   string
		output string
		dir    string
		kind   string
	}{
		{name: "denied socket", kind: "docker_permission_denied",
			output: "#docker\n/usr/bin/docker\n#daemon\npermission denied while trying to connect to the docker API at unix:///var/run/docker.sock\n#compose\n5.3.1\n#dir\npresent\n"},
		{name: "no docker", kind: "docker_unavailable",
			output: "#docker\n#daemon\n#compose\n#legacy\n#dir\npresent\n"},
		{name: "compose v1", kind: "compose_unavailable",
			output: "#docker\n/usr/bin/docker\n#daemon\n20.10.24\n#compose\n#legacy\n/usr/bin/docker-compose\n#dir\npresent\n"},
		{name: "project gone", kind: "compose_dir_missing", dir: "/srv/gone",
			output: "#docker\n/usr/bin/docker\n#daemon\n29.7.0\n#compose\n5.3.1\n#legacy\n#dir\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prober := fakeProber{result: probe.Parse([]byte(test.output), test.dir)}
			failure := ComposePreflight(context.Background(),
				Invocation{Command: CommandStatus, Host: "production"}, prober)
			if failure == nil {
				t.Fatal("the command was allowed to run and fail opaquely instead")
			}
			if failure.Kind != test.kind {
				t.Errorf("Kind = %q, want %q", failure.Kind, test.kind)
			}
			if failure.Message == "" || failure.ExitCode != 1 {
				t.Errorf("failure = %+v", failure)
			}
		})
	}
}

// Nothing established rejects nothing. A probe that could not run, or a host
// that answered nothing, leaves the command to run and report its own failure
// — which is where this whole mechanism started, and is never worse than it.
func TestComposePreflightRejectsNothingItDidNotEstablish(t *testing.T) {
	working := probe.Parse([]byte("#docker\n/usr/bin/docker\n#daemon\n29.7.0\n#compose\n5.3.1\n#dir\npresent\n"), "/srv/app")
	for name, prober := range map[string]fakeProber{
		"working host": {result: working},
		"empty answer": {},
		"probe failed": {err: errors.New("connection lost")},
	} {
		if failure := ComposePreflight(context.Background(),
			Invocation{Command: CommandStatus}, prober); failure != nil {
			t.Errorf("%s: rejected with %+v", name, failure)
		}
	}
}

// A configured script is a command the operator wrote. It has never needed a
// daemon, and a host without one is still a host worth running it on.
func TestComposePreflightLetsScriptsThrough(t *testing.T) {
	prober := fakeProber{result: probe.Parse([]byte("#docker\n#daemon\n#compose\n#legacy\n"), "")}
	if failure := ComposePreflight(context.Background(),
		Invocation{Command: CommandScript, ScriptName: "deploy"}, prober); failure != nil {
		t.Errorf("a script was refused for want of compose: %+v", failure)
	}
	// And a compose command on the same host is not.
	if failure := ComposePreflight(context.Background(),
		Invocation{Command: CommandRestart, Service: "api"}, prober); failure == nil {
		t.Error("a restart was allowed on a host with no docker")
	}
}

type fakeProber struct {
	result probe.Result
	err    error
}

func (f fakeProber) Probe(context.Context) (probe.Result, error) { return f.result, f.err }

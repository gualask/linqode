package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/gualask/linqode/internal/operations"
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

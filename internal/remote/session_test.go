package remote_test

// Integration tests: the production client against the in-process scripted
// server. Every test spawns its own server with its own temp dir and wraps
// waits in a guard timeout.

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/gualask/linqode/internal/remote"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	t.Cleanup(cancel)
	return ctx
}

func connect(t *testing.T, server *testServer, p remote.Prompter) *remote.Session {
	t.Helper()
	session, err := remote.ConnectWith(testContext(t), server.target(), p, server.options())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

func TestTOFUAcceptsPersistsAndReconnectsSilently(t *testing.T) {
	server := spawn(t, map[string]script{
		"echo hello": {stdout: []string{"hello\n"}},
	})

	accepting := &prompter{acceptHostKey: true}
	session := connect(t, server, accepting)
	if got := accepting.hostKeyPrompts.Load(); got != 1 {
		t.Errorf("prompted %d times, want 1", got)
	}

	out, err := session.Exec(testContext(t), "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Stdout) != "hello\n" || out.ExitCode != 0 {
		t.Errorf("got %+v", out)
	}

	raw, err := os.ReadFile(server.knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	wantAddr := knownhosts.Normalize("127.0.0.1:" + strconv.Itoa(int(server.port)))
	if !strings.Contains(string(raw), wantAddr) {
		t.Fatalf("known_hosts %q does not pin %q", raw, wantAddr)
	}

	// Reconnecting must not prompt again.
	silent := &prompter{}
	connect(t, server, silent)
	if got := silent.hostKeyPrompts.Load(); got != 0 {
		t.Errorf("reconnect prompted %d times, want 0", got)
	}
}

func TestRefusedHostKeyAbortsConnect(t *testing.T) {
	server := spawn(t, nil)

	_, err := remote.ConnectWith(testContext(t), server.target(), &prompter{}, server.options())
	var rejected *remote.HostKeyRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("got %v, want HostKeyRejectedError", err)
	}
}

func TestExecCollectsStdoutStderrAndExitCode(t *testing.T) {
	server := spawn(t, map[string]script{
		"report": {
			stdout: []string{"chunk one ", "chunk two"},
			stderr: []string{"warning: ", "sample"},
			exit:   3,
		},
	})
	session := connect(t, server, &prompter{acceptHostKey: true})

	out, err := session.Exec(testContext(t), "report")
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Stdout) != "chunk one chunk two" {
		t.Errorf("stdout %q", out.Stdout)
	}
	if string(out.Stderr) != "warning: sample" {
		t.Errorf("stderr %q", out.Stderr)
	}
	if out.ExitCode != 3 {
		t.Errorf("exit %d, want 3", out.ExitCode)
	}
}

func TestExecStreamDeliversEventsThenEnds(t *testing.T) {
	server := spawn(t, map[string]script{
		"emit": {stdout: []string{"a", "b"}, stderr: []string{"e"}, exit: 5},
	})
	session := connect(t, server, &prompter{acceptHostKey: true})

	events, err := session.ExecStream(testContext(t), "emit")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	exit := -1
	for ev := range events {
		switch ev.Kind {
		case remote.ExecStdout:
			stdout.Write(ev.Data)
		case remote.ExecStderr:
			stderr.Write(ev.Data)
		case remote.ExecExit:
			exit = ev.ExitCode
		}
	}
	// The channel closed: everything arrived.
	if stdout.String() != "ab" || stderr.String() != "e" || exit != 5 {
		t.Errorf("stdout %q stderr %q exit %d", stdout.String(), stderr.String(), exit)
	}
}

func TestExecStreamCancelEndsAFollower(t *testing.T) {
	server := spawn(t, map[string]script{
		"follow": {stdout: []string{"line 1\n"}, holdOpen: true},
	})
	session := connect(t, server, &prompter{acceptHostKey: true})

	ctx, cancel := context.WithCancel(testContext(t))
	events, err := session.ExecStream(ctx, "follow")
	if err != nil {
		t.Fatal(err)
	}

	// The follower streams while running…
	ev, ok := <-events
	if !ok || ev.Kind != remote.ExecStdout || string(ev.Data) != "line 1\n" {
		t.Fatalf("got %+v ok=%v", ev, ok)
	}

	// …and cancelling ends the event stream instead of hanging.
	cancel()
	for range events {
		// drain in-flight events; the guard timeout catches a hang
	}
}

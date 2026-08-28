//go:build e2e

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/remote"
)

// TestComposeStatusReportsDemoProject proves the ps command Linqode builds
// parses into the model the status view renders, against a live daemon —
// including the states and health values the offline suite only ever saw as
// captured fixtures.
func TestComposeStatusReportsDemoProject(t *testing.T) {
	session := connect(t)

	// Health takes a few probe intervals to settle, so poll rather than
	// asserting on the first sample.
	services := pollServices(t, session, 90*time.Second, func(services []compose.Service) bool {
		byName := index(services)
		return byName["db"].Health == "healthy" && byName["cache"].Health == "unhealthy"
	})

	byName := index(services)
	for _, name := range []string{"api", "cache", "db", "migrate", "web"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("service %q missing from ps output", name)
		}
	}

	// ParsePS sorts by service name; the view depends on that order being
	// stable across refreshes.
	var order []string
	for _, s := range services {
		order = append(order, s.Service)
	}
	if !isSorted(order) {
		t.Errorf("services not sorted by name: %v", order)
	}

	if state := byName["web"].State; state != "running" {
		t.Errorf("web state = %q, want running", state)
	}
	// `--all` is what makes an exited one-shot service visible at all.
	if state := byName["migrate"].State; state != "exited" {
		t.Errorf("migrate state = %q, want exited (is --all being passed?)", state)
	}
	if code := byName["migrate"].ExitCode; code != 0 {
		t.Errorf("migrate exit code = %d, want 0", code)
	}
	// The published port must survive parsing into the summary the table
	// shows, with compose's IPv4/IPv6 duplicates collapsed.
	web := byName["web"]
	if ports := web.PortsSummary(); !strings.Contains(ports, "8080->80/tcp") {
		t.Errorf("web ports = %q, want it to contain 8080->80/tcp", ports)
	}
}

// TestFollowPlainTextLogs proves an unstructured service streams through the
// line assembler as readable lines, and is not mistaken for JSONL.
func TestFollowPlainTextLogs(t *testing.T) {
	session := connect(t)

	lines := followLines(t, session, compose.LogsCommand(composeDir, "web", 10, true), 3)
	for _, line := range lines {
		if !strings.Contains(line, "web: request served") {
			t.Errorf("unexpected log line %q", line)
		}
		if record := logs.ParseRecord(line); record != nil {
			t.Errorf("plain-text line parsed as a record: %q", line)
		}
	}
}

// TestFollowStructuredLogs proves JSONL emitted by a real container reaches
// the engine intact and flattens the way the structured view expects.
func TestFollowStructuredLogs(t *testing.T) {
	session := connect(t)

	lines := followLines(t, session, compose.LogsCommand(composeDir, "api", 10, true), 5)

	levels := map[string]int{}
	for _, line := range lines {
		record := logs.ParseRecord(line)
		if record == nil {
			t.Fatalf("JSONL line did not parse as a record: %q", line)
		}
		level, ok := record.Level()
		if !ok {
			t.Errorf("record has no level: %q", line)
			continue
		}
		levels[level]++

		if _, ok := record.Message(); !ok {
			t.Errorf("record has no message: %q", line)
		}
		if _, ok := record.Timestamp(); !ok {
			t.Errorf("record has no timestamp: %q", line)
		}
		// The nested object must flatten to a dotted path — the property the
		// whole field-filter feature rests on.
		if status, ok := record.Get("http.status"); !ok {
			t.Errorf("record missing flattened http.status: %q", line)
		} else if status != "200" && status != "500" && status != "429" {
			t.Errorf("http.status = %q, unexpected", status)
		}
		if service, _ := record.Get("service"); service != "api" {
			t.Errorf("service field = %q, want api", service)
		}
	}
	if len(levels) == 0 {
		t.Fatal("no levels parsed")
	}
}

// TestCancelEndsFollower covers a gap the in-process server cannot: that
// cancelling a follow actually tears the remote `docker compose logs -f`
// down against a real sshd, instead of leaking it.
func TestCancelEndsFollower(t *testing.T) {
	session := connect(t)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := session.ExecStream(ctx, compose.LogsCommand(composeDir, "web", 5, true))
	if err != nil {
		t.Fatalf("starting follower: %v", err)
	}

	// Wait for real output, so the follower is definitely running.
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("stream closed before any output")
		}
		if event.Kind != remote.ExecStdout {
			t.Logf("first event kind %v", event.Kind)
		}
	case <-time.After(guardTimeout):
		cancel()
		t.Fatal("no output from follower")
	}

	// Detection sanity check: if the pattern matched nothing even while the
	// follower is demonstrably running, the post-cancel assertion below
	// would pass vacuously.
	if pids := followerPIDs(t, session); pids == "" {
		t.Fatal("follower not visible in the remote process list while it is streaming")
	}

	cancel()

	// Draining must end promptly; a leaked follower would keep it open.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range events {
		}
	}()
	select {
	case <-drained:
	case <-time.After(guardTimeout):
		t.Fatal("event stream did not close after cancel")
	}

	// The client unblocking is not proof the server let go: check the remote
	// process is actually gone, which is what SIGTERM-then-close is for.
	deadline := time.Now().Add(20 * time.Second)
	for {
		remaining := followerPIDs(t, session)
		if remaining == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("follower still running %s after cancel (pids %q)",
				time.Since(deadline.Add(-20*time.Second)).Round(time.Second), remaining)
		}
		time.Sleep(time.Second)
	}
}

// followerPIDs lists the remote pids of a running `compose logs` follower.
//
// The bracket in `[c]ompose` keeps the pattern from matching the shell that
// runs this very command, whose own command line would otherwise contain the
// search string. TestCancelEndsFollower asserts this returns something while
// the follower runs, so a pattern that silently matches nothing cannot make
// the leak check pass by accident.
func followerPIDs(t *testing.T, session *remote.Session) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	out, err := session.Exec(ctx, "pgrep -f '[c]ompose logs' || true")
	if err != nil {
		t.Fatalf("listing follower processes: %v", err)
	}
	return strings.TrimSpace(string(out.Stdout))
}

// TestRestartServiceRestartsContainer proves the action command runs and the
// service comes back, which is what the status view refreshes to show.
func TestRestartServiceRestartsContainer(t *testing.T) {
	session := connect(t)

	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	out, err := session.Exec(ctx, compose.ActionCommand(composeDir, compose.ActionRestart, "web"))
	if err != nil {
		t.Fatalf("restarting web: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("restart exited %d: %s", out.ExitCode, out.Stderr)
	}

	pollServices(t, session, 60*time.Second, func(services []compose.Service) bool {
		return index(services)["web"].State == "running"
	})
}

// TestTOFUPersistsHostKey proves trust-on-first-use against a real sshd: the
// first connection prompts once and records the key, the second is silent.
func TestTOFUPersistsHostKey(t *testing.T) {
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	prompter := &countingPrompter{}

	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
		session, err := remote.ConnectWith(ctx, fixtureTarget(), prompter, remote.ConnectOptions{
			KnownHostsFile: knownHosts,
			IdentitiesOnly: true,
		})
		cancel()
		if err != nil {
			t.Fatalf("connect %d: %v", i+1, err)
		}
		session.Close()
	}

	if got := prompter.hostKeyPrompts.Load(); got != 1 {
		t.Errorf("host key prompts = %d, want exactly 1 (TOFU should persist)", got)
	}
}

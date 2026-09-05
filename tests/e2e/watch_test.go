//go:build e2e

package e2e

// The daemon's event stream against a real daemon.
//
// Two things are worth proving here that a unit test cannot. That the filters
// actually filter: the demo project runs two health-checked services probing
// every two seconds, and an unfiltered stream would bury a restart under
// `exec_create`/`exec_start`/`exec_die` — thirty events for every useful one,
// which is more traffic on an idle project than the polling this replaces.
// And that a change reaches the client promptly, without waiting for anything
// to be asked.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/remote"
)

func TestWatchReportsProjectChanges(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The project name comes from `ps`, the way the screen gets it.
	services := servicesNow(t, session)
	project := services[0].Project
	if project == "" {
		t.Fatal("ps did not report the compose project name")
	}

	events, err := session.ExecStream(ctx, compose.EventsCommand(project))
	if err != nil {
		t.Fatalf("starting the event stream: %v", err)
	}

	// Give the stream a moment to attach before making something happen:
	// `docker events` reports what happens after it starts.
	time.Sleep(2 * time.Second)

	// A container the project knows nothing about, started the way anything
	// else on a shared host would be. Its events must not reach this
	// session: `docker events` reports the whole daemon, and the label
	// filter is the only thing scoping the stream to these containers.
	runOutsideProject(t, session, "linqode-e2e-noise")

	restart, cancelRestart := context.WithTimeout(context.Background(), guardTimeout)
	defer cancelRestart()
	out, err := session.Exec(restart, compose.ActionCommand(composeDir, compose.ActionRestart, "web"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("restarting web: %v (exit %d)\n%s", err, out.ExitCode, out.Stderr)
	}

	var assembler logs.LineAssembler
	seen := map[string]int{}
	deadline := time.After(30 * time.Second)

collect:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				break collect
			}
			switch event.Kind {
			case remote.ExecStdout:
				for _, line := range assembler.Push(event.Data) {
					change, ok := compose.ParseEvent(line)
					if !ok {
						t.Errorf("unparsed event line: %q", line)
						continue
					}
					if strings.HasPrefix(change.Kind(), "exec_") {
						t.Errorf("health-check noise reached the client: %q", line)
					}
					if strings.Contains(change.Container, "noise") {
						t.Errorf("an event from outside the project reached the client: %q", line)
					}
					// The time is the daemon's own. Only a real daemon can
					// say whether `{{.Time}}` is a field it fills in, and
					// with what — the last format detail that looked
					// obvious cost an afternoon (`\t`, which docker prints
					// as two characters).
					if change.At.IsZero() {
						t.Errorf("the daemon reported no time for %q", line)
					} else if drift := time.Since(change.At); drift < -time.Minute || drift > time.Minute {
						t.Errorf("event %q is stamped %v away from now", line, drift)
					}
					if !strings.Contains(change.Container, "web") {
						continue
					}
					seen[change.Kind()]++
				}
			case remote.ExecStderr:
				t.Logf("stderr: %s", event.Data)
			case remote.ExecExit:
				t.Fatalf("the event stream exited early with code %d", event.ExitCode)
			}
			// A restart is a die and a start; either one is enough to know
			// the table would have been re-read.
			if seen["start"] > 0 && seen["die"] > 0 {
				break collect
			}
		case <-deadline:
			break collect
		}
	}

	if seen["start"] == 0 || seen["die"] == 0 {
		t.Fatalf("restarting web produced %v, want both a die and a start", seen)
	}
}

// Closing the stream must terminate the remote command: a `docker events`
// left running on the server would outlive every session that opened one.
func TestWatchStopsWithItsContext(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithCancel(context.Background())

	services := servicesNow(t, session)
	events, err := session.ExecStream(ctx, compose.EventsCommand(services[0].Project))
	if err != nil {
		t.Fatalf("starting the event stream: %v", err)
	}
	cancel()

	deadline := time.After(guardTimeout)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the event stream outlived its context")
		}
	}
}

// runOutsideProject starts and stops a container that belongs to no compose
// project, so the stream has something to ignore. It is removed before the
// test returns whatever happens: a container left behind would show up in the
// next run's `ps`.
func runOutsideProject(t *testing.T, session *remote.Session, name string) {
	t.Helper()
	run := func(command string) {
		ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
		defer cancel()
		if _, err := session.Exec(ctx, command); err != nil {
			t.Fatalf("running %q: %v", command, err)
		}
	}
	t.Cleanup(func() { run("docker rm -f " + name + " >/dev/null 2>&1 || true") })
	run("docker rm -f " + name + " >/dev/null 2>&1 || true")
	run("docker run -d --name " + name + " busybox sh -c 'sleep 2'")
}

// servicesNow reads the project's services once, for the fields a test needs
// from them.
func servicesNow(t *testing.T, session *remote.Session) []compose.Service {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()
	out, err := session.Exec(ctx, compose.PsCommand(composeDir))
	if err != nil {
		t.Fatalf("listing services: %v", err)
	}
	services, err := compose.ParsePS(out.Stdout)
	if err != nil {
		t.Fatalf("parsing services: %v\noutput was:\n%s", err, out.Stdout)
	}
	if len(services) == 0 {
		t.Fatal("the demo project reported no services")
	}
	return services
}

//go:build e2e

package e2e

import (
	"context"
	"flag"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/remote"
)

// Measuring what candidate dashboard commands cost on the server, so the
// refresh design is decided on numbers rather than intuition. Opt-in: it
// adds seconds to a run and asserts nothing.
//
//	go test -tags e2e ./tests/e2e/ -run TestRemoteCommandCost -cost.measure
//
// Add -fixture.keep and inflate the container count by hand between runs to
// see how each command scales.
var measureCost = flag.Bool("cost.measure", false, "measure the cost of candidate dashboard commands")

const costSamples = 10

func TestRemoteCommandCost(t *testing.T) {
	if !*measureCost {
		t.Skip("opt-in: pass -cost.measure")
	}
	session := connect(t)

	// `true` isolates the fixed overhead every exec pays — opening a channel
	// and forking a shell — so the rest can be read as the command's own
	// cost.
	cases := []struct {
		label   string
		command string
	}{
		{"baseline (exec overhead)", "true"},
		{"host metrics", "uptime && free -m && df -h /"},
		{"compose ps (current refresh)", compose.PsCommand(composeDir)},
		{"docker stats --no-stream", "docker stats --no-stream --format '{{json .}}'"},
		{"container cgroups", compose.StatsCgroupCommand(runningPids(t, session))},
	}
	// The event stream is deliberately absent: a stream has no round-trip to
	// measure, and what it costs is the traffic it carries, which the watch
	// tests exercise instead.

	running := strings.TrimSpace(string(execOrFail(t, session, "docker ps -q | wc -l").Stdout))
	t.Logf("containers running: %s", running)
	t.Logf("%-30s %8s %8s %8s %8s", "command", "min", "median", "max", "bytes")

	for _, c := range cases {
		var durations []time.Duration
		var size int
		for range costSamples {
			start := time.Now()
			out := execOrFail(t, session, c.command)
			durations = append(durations, time.Since(start))
			size = len(out.Stdout)
		}
		slices.Sort(durations)
		t.Logf("%-30s %8s %8s %8s %8d",
			c.label,
			round(durations[0]),
			round(durations[len(durations)/2]),
			round(durations[len(durations)-1]),
			size)
	}
}

func execOrFail(t *testing.T, session *remote.Session, command string) remote.ExecOutput {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := session.Exec(ctx, command)
	if err != nil {
		t.Fatalf("running %q: %v", command, err)
	}
	return out
}

// runningPids is what the cgroup sample needs to reach the network counters:
// the same pids the refresh already learns from its inspect.
func runningPids(t *testing.T, session *remote.Session) []int {
	t.Helper()
	out := execOrFail(t, session, "docker inspect --format '{{.State.Pid}}' $(docker ps -q)")
	var pids []int
	for _, line := range strings.Fields(string(out.Stdout)) {
		if pid, err := strconv.Atoi(line); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func round(d time.Duration) string {
	return fmt.Sprintf("%v", d.Round(time.Millisecond))
}

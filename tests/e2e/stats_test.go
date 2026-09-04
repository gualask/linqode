//go:build e2e

package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/remote"
)

func TestHostMetricsAgainstRealHost(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()
	out, err := session.Exec(ctx, host.Command())
	if err != nil {
		t.Fatalf("sampling host metrics: %v", err)
	}
	metrics, err := host.Parse(out.Stdout)
	if err != nil {
		t.Fatalf("parsing host metrics: %v\noutput was:\n%s", err, out.Stdout)
	}
	if metrics.CPUs < 1 {
		t.Errorf("CPUs = %d, want at least 1", metrics.CPUs)
	}
	if metrics.MemTotalKB == 0 || metrics.MemAvailableKB == 0 {
		t.Error("memory metrics not reported")
	}
	if metrics.DiskTotalKB == 0 {
		t.Errorf("DiskTotalKB = 0\noutput was:\n%s", out.Stdout)
	}
	if metrics.Uptime <= 0 {
		t.Error("Uptime not reported")
	}
	if percent := metrics.MemUsedPercent(); percent <= 0 || percent > 100 {
		t.Errorf("MemUsedPercent = %v, outside 0–100", percent)
	}
	if percent := metrics.DiskUsedPercent(); percent <= 0 || percent > 100 {
		t.Errorf("DiskUsedPercent = %v, outside 0–100", percent)
	}
}

func TestStatsStreamAgainstRealProject(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := session.ExecStream(ctx, compose.StatsCommand(composeDir))
	if err != nil {
		t.Fatalf("starting stats stream: %v", err)
	}
	seen := map[string]compose.ContainerStats{}
	var assembler logs.LineAssembler
	deadline := time.After(45 * time.Second)

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
					if stats, ok := compose.ParseStats(line); ok {
						seen[stats.Name] = stats
					}
				}
			case remote.ExecStderr:
				t.Logf("stderr: %s", event.Data)
			case remote.ExecExit:
				t.Fatalf("stats stream exited early with code %d", event.ExitCode)
			}
			if len(seen) >= demoRunningServices {
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if len(seen) < demoRunningServices {
		t.Fatalf("saw %d containers, want %d: %v", len(seen), demoRunningServices, keys(seen))
	}
	for name, stats := range seen {
		assertStats(t, name, stats)
	}
}

func TestStatsSampleAgainstRealProject(t *testing.T) {
	session := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()
	start := time.Now()
	out, err := session.Exec(ctx, compose.StatsSampleCommand(composeDir))
	if err != nil {
		t.Fatalf("sampling stats: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("stats sample exited %d: %s", out.ExitCode, out.Stderr)
	}
	t.Logf("one sample took %v for %d containers", time.Since(start).Round(time.Millisecond), demoRunningServices)
	sample := compose.ParseStatsSample(out.Stdout)
	if len(sample) != demoRunningServices {
		t.Fatalf("sample holds %d readings, want %d: %+v", len(sample), demoRunningServices, sample)
	}
	for _, stats := range sample {
		assertStats(t, stats.Name, stats)
	}
}

func assertStats(t *testing.T, name string, stats compose.ContainerStats) {
	t.Helper()
	if !strings.HasPrefix(name, "demo-") {
		t.Errorf("stats reported a container outside the project: %q", name)
	}
	if _, ok := stats.CPUPercent(); !ok {
		t.Errorf("%s: unparseable CPU reading %q", name, stats.CPUPerc)
	}
	if _, ok := stats.MemPercent(); !ok {
		t.Errorf("%s: unparseable memory reading %q", name, stats.MemPerc)
	}
	if stats.MemAmount() == "" {
		t.Errorf("%s: empty memory amount from %q", name, stats.MemUsage)
	}
	// The I/O readings are pairs, and the table prints both halves. Only
	// this layer sees docker's real output, so it is the only place a
	// release that changed "a / b" into something else would be caught.
	for _, pair := range []struct{ label, raw, shown string }{
		{"network", stats.NetIO, stats.NetAmount()},
		{"block I/O", stats.BlockIO, stats.BlockAmount()},
	} {
		if !strings.Contains(pair.shown, "/") {
			t.Errorf("%s: %s reading %q is not a pair (from %q)",
				name, pair.label, pair.shown, pair.raw)
		}
		if strings.Contains(pair.shown, " ") {
			t.Errorf("%s: %s reading %q kept docker's spacing, which reads as a column break",
				name, pair.label, pair.shown)
		}
	}
}

func keys(m map[string]compose.ContainerStats) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

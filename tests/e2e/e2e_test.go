//go:build e2e

package e2e

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
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

	lines := followLines(t, session, compose.LogsCommand(composeDir, "web", 10), 3)
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

	lines := followLines(t, session, compose.LogsCommand(composeDir, "api", 10), 5)

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
	events, err := session.ExecStream(ctx, compose.LogsCommand(composeDir, "web", 5))
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

// TestHostMetricsAgainstRealHost proves the header's metrics command works
// on a real (busybox) userland: /proc layout and `df -Pk` support are the
// parts most likely to differ from a developer's machine.
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

	// Every field must actually arrive: a silently empty section would make
	// the header quietly useless.
	if metrics.CPUs < 1 {
		t.Errorf("CPUs = %d, want at least 1 (is /proc/cpuinfo readable?)", metrics.CPUs)
	}
	if metrics.MemTotalKB == 0 {
		t.Error("MemTotalKB = 0 (is /proc/meminfo readable?)")
	}
	if metrics.MemAvailableKB == 0 {
		t.Error("MemAvailableKB = 0 (does this kernel report MemAvailable?)")
	}
	if metrics.DiskTotalKB == 0 {
		t.Errorf("DiskTotalKB = 0 (does this df support -Pk?)\noutput was:\n%s", out.Stdout)
	}
	if metrics.Uptime <= 0 {
		t.Error("Uptime not reported")
	}
	// Derived values must be sane, not just non-zero.
	if percent := metrics.MemUsedPercent(); percent <= 0 || percent > 100 {
		t.Errorf("MemUsedPercent = %v, outside 0–100", percent)
	}
	if percent := metrics.DiskUsedPercent(); percent <= 0 || percent > 100 {
		t.Errorf("DiskUsedPercent = %v, outside 0–100", percent)
	}
}

// TestStatsStreamAgainstRealProject proves the resource columns' command
// streams parseable samples for the project's containers — and only those.
func TestStatsStreamAgainstRealProject(t *testing.T) {
	session := connect(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := session.ExecStream(ctx, compose.StatsCommand(composeDir))
	if err != nil {
		t.Fatalf("starting stats stream: %v", err)
	}

	// Collect until every demo service has reported once, or we run out of
	// patience: docker needs a couple of seconds for its first sample.
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
		// Scoped to the project: `migrate` has exited, and nothing outside
		// the compose project should appear.
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
	}
}

// TestStatsSampleAgainstRealProject proves the periodic refresh's command —
// the one-shot form behind the table's CPU and MEM columns — terminates on
// its own and parses into a whole sample.
//
// It also puts a number on what that costs: the log line is the evidence
// behind the 20 s interval, since the sampling latency is docker's and no
// amount of client-side care can shorten it.
func TestStatsSampleAgainstRealProject(t *testing.T) {
	session := connect(t)

	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	start := time.Now()
	out, err := session.Exec(ctx, compose.StatsSampleCommand(composeDir))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sampling stats: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("stats sample exited %d: %s", out.ExitCode, out.Stderr)
	}
	t.Logf("one sample took %v for %d containers", elapsed.Round(time.Millisecond), demoRunningServices)

	sample := compose.ParseStatsSample(out.Stdout)
	if len(sample) != demoRunningServices {
		t.Fatalf("sample holds %d readings, want %d: %+v", len(sample), demoRunningServices, sample)
	}
	for _, stats := range sample {
		// Scoped to the project: `migrate` has exited, and nothing outside
		// the compose project should appear.
		if !strings.HasPrefix(stats.Name, "demo-") {
			t.Errorf("sample reported a container outside the project: %q", stats.Name)
		}
		if _, ok := stats.CPUPercent(); !ok {
			t.Errorf("%s: unparseable CPU reading %q", stats.Name, stats.CPUPerc)
		}
		if _, ok := stats.MemPercent(); !ok {
			t.Errorf("%s: unparseable memory reading %q", stats.Name, stats.MemPerc)
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

// followLines collects want complete lines from a streaming command through
// the production line assembler, then cancels.
func followLines(t *testing.T, session *remote.Session, command string, want int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	events, err := session.ExecStream(ctx, command)
	if err != nil {
		t.Fatalf("starting %q: %v", command, err)
	}

	var assembler logs.LineAssembler
	var lines []string
	for event := range events {
		switch event.Kind {
		case remote.ExecStdout:
			for _, line := range assembler.Push(event.Data) {
				if strings.TrimSpace(line) == "" {
					continue
				}
				lines = append(lines, line)
				if len(lines) >= want {
					return lines
				}
			}
		case remote.ExecStderr:
			t.Logf("stderr: %s", event.Data)
		case remote.ExecExit:
			t.Fatalf("follower exited early with code %d", event.ExitCode)
		}
	}
	t.Fatalf("stream ended with %d/%d lines", len(lines), want)
	return nil
}

// pollServices refreshes `compose ps` until done is satisfied, returning the
// services that satisfied it.
func pollServices(t *testing.T, session *remote.Session, timeout time.Duration, done func([]compose.Service) bool) []compose.Service {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []compose.Service
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
		out, err := session.Exec(ctx, compose.PsCommand(composeDir))
		cancel()
		if err != nil {
			t.Fatalf("compose ps: %v", err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("compose ps exited %d: %s", out.ExitCode, out.Stderr)
		}
		services, err := compose.ParsePS(out.Stdout)
		if err != nil {
			t.Fatalf("parsing ps output: %v\noutput was: %s", err, out.Stdout)
		}
		last = services
		if done(services) {
			return services
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("condition not met within %s; last state: %s", timeout, summarize(last))
	return nil
}

func index(services []compose.Service) map[string]compose.Service {
	byName := make(map[string]compose.Service, len(services))
	for _, s := range services {
		byName[s.Service] = s
	}
	return byName
}

func summarize(services []compose.Service) string {
	var parts []string
	for _, s := range services {
		parts = append(parts, s.Service+"="+s.State+"/"+s.Health)
	}
	return strings.Join(parts, " ")
}

func isSorted(names []string) bool {
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			return false
		}
	}
	return true
}

// countingPrompter accepts everything and counts how often it is asked.
type countingPrompter struct {
	hostKeyPrompts atomic.Int32
	passPrompts    atomic.Int32
}

func (p *countingPrompter) ConfirmHostKey(string, uint16, string, string) (bool, error) {
	p.hostKeyPrompts.Add(1)
	return true, nil
}

func (p *countingPrompter) AskPassphrase(string) (string, error) {
	p.passPrompts.Add(1)
	return "", nil
}

// TestRestartCountsAgainstRealProject covers what no offline test can: that
// the `docker inspect` format behind the RESTARTS column is understood by a
// real daemon, and that the counts it returns line up with the container
// names `compose ps` reported.
//
// `flaky` is what makes this more than a format check. RestartCount tracks
// the restarts docker performs under the restart policy, so only a service
// that fails on its own produces a non-zero one — restarting a service by
// hand, as TestRestartServiceRestartsContainer does, leaves it at zero.
func TestRestartCountsAgainstRealProject(t *testing.T) {
	session := connect(t)

	// The policy takes a moment to exhaust its retries; until then the count
	// is still climbing.
	var services []compose.Service
	deadline := time.Now().Add(60 * time.Second)
	for {
		services = pollServices(t, session, guardTimeout, func([]compose.Service) bool { return true })

		names := make([]string, 0, len(services))
		for _, service := range services {
			names = append(names, service.Name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
		out, err := session.Exec(ctx, compose.InspectRestartsCommand(names))
		cancel()
		if err != nil {
			t.Fatalf("docker inspect: %v", err)
		}
		compose.ApplyRestarts(services, compose.ParseRestarts(out.Stdout))

		byName := index(services)
		if flaky := byName["flaky"]; flaky.Restarts != nil && *flaky.Restarts >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("flaky never reached its 3 policy restarts: %s", summarizeRestarts(services))
		}
		time.Sleep(2 * time.Second)
	}

	// Every container `ps` named must have been matched: a name the parser
	// failed to line up (docker prints it with a leading slash) would leave
	// the column reading `-` for a host that answered perfectly well.
	for _, service := range services {
		if service.Restarts == nil {
			t.Errorf("%s got no restart count, table would show %q",
				service.Service, service.RestartsText())
		}
	}
	// The services that never fail must report a genuine zero, not just
	// something non-nil.
	byName := index(services)
	for _, name := range []string{"web", "api", "db"} {
		service := byName[name]
		if got := service.RestartsText(); got != "0" {
			t.Errorf("%s restarts = %s, want 0", name, got)
		}
	}
	t.Logf("restart counts: %s", summarizeRestarts(services))
}

func summarizeRestarts(services []compose.Service) string {
	var parts []string
	for _, service := range services {
		parts = append(parts, service.Service+"="+service.RestartsText())
	}
	return strings.Join(parts, " ")
}

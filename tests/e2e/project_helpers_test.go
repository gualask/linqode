//go:build e2e

package e2e

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/remote"
)

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
	for _, service := range services {
		byName[service.Service] = service
	}
	return byName
}

func summarize(services []compose.Service) string {
	var parts []string
	for _, service := range services {
		parts = append(parts, service.Service+"="+service.State+"/"+service.Health)
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

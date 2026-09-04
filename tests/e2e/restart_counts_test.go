//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
)

func TestRestartCountsAgainstRealProject(t *testing.T) {
	session := connect(t)
	var services []compose.Service
	deadline := time.Now().Add(60 * time.Second)
	for {
		services = pollServices(t, session, guardTimeout, func([]compose.Service) bool { return true })
		names := make([]string, 0, len(services))
		for _, service := range services {
			names = append(names, service.Name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
		out, err := session.Exec(ctx, compose.InspectCommand(names))
		cancel()
		if err != nil {
			t.Fatalf("docker inspect: %v", err)
		}
		compose.ApplyInspected(services, compose.ParseInspected(out.Stdout))
		if flaky := index(services)["flaky"]; flaky.Restarts != nil && *flaky.Restarts >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("flaky never reached its 3 policy restarts: %s", summarizeRestarts(services))
		}
		time.Sleep(2 * time.Second)
	}
	for _, service := range services {
		if service.Restarts == nil {
			t.Errorf("%s got no restart count, table would show %q", service.Service, service.RestartsText())
		}
	}
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

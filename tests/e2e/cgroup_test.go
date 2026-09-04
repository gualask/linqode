//go:build e2e

package e2e

// The kernel's container counters against a real daemon.
//
// What only a real host can answer: that the globs find the cgroup layout the
// daemon actually uses, that the ids in those paths are the ids `ps` reports,
// and that the counters are readable by the unprivileged account an operator
// connects as — including `/proc/<pid>/net/dev`, which is the one reading that
// looks like it should need root and does not.

import (
	"context"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/remote"
)

func TestContainerCgroupsAgainstRealProject(t *testing.T) {
	session := connect(t)
	services := runningServices(t, session)

	before := cgroupSample(t, session, services)
	if len(before.Containers) == 0 {
		t.Fatal("no container counters found: the cgroup layout was not recognised")
	}

	// Every running service must be in there under the id `ps` gave it.
	for _, service := range services {
		reading, ok := before.Reading(service.ID)
		if !ok {
			t.Errorf("%s (%s) has no counters", service.Service, service.ID)
			continue
		}
		if reading.MemBytes == 0 {
			t.Errorf("%s reports no memory in use", service.Service)
		}
		if service.Pid > 0 {
			if _, ok := before.Networks[service.Pid]; !ok {
				t.Errorf("%s has no network counters for pid %d", service.Service, service.Pid)
			}
		}
	}

	// A percentage takes two readings, which is the whole point: the server
	// is not asked to wait a second on our behalf.
	time.Sleep(2 * time.Second)
	after := cgroupSample(t, session, services)

	stats := before.Delta(after, services, 0)
	if len(stats) != len(services) {
		t.Fatalf("%d readings for %d services", len(stats), len(services))
	}
	for _, reading := range stats {
		percent, ok := reading.CPUPercent()
		if !ok {
			t.Errorf("%s: CPU percentage not derived (%q)", reading.Name, reading.CPUPerc)
			continue
		}
		if percent < 0 || percent > 10_000 {
			t.Errorf("%s: implausible CPU percentage %v", reading.Name, percent)
		}
		if reading.MemAmount() == "" || reading.MemAmount() == "0B" {
			t.Errorf("%s: no memory reading (%q)", reading.Name, reading.MemUsage)
		}
	}
}

func cgroupSample(t *testing.T, session *remote.Session, services []compose.Service) compose.CgroupSample {
	t.Helper()
	pids := make([]int, 0, len(services))
	for _, service := range services {
		if service.Pid > 0 {
			pids = append(pids, service.Pid)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()
	out, err := session.Exec(ctx, compose.StatsCgroupCommand(pids))
	if err != nil {
		t.Fatalf("sampling container cgroups: %v", err)
	}
	return compose.ParseCgroupSample(out.Stdout, time.Now())
}

// runningServices is the project's running containers, enriched the way the
// status refresh enriches them — the ids come from `ps`, the pids from the
// inspect that follows it.
func runningServices(t *testing.T, session *remote.Session) []compose.Service {
	t.Helper()
	services := servicesNow(t, session)

	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()
	out, err := session.Exec(ctx, compose.InspectCommand(names))
	if err != nil {
		t.Fatalf("inspecting containers: %v", err)
	}
	compose.ApplyInspected(services, compose.ParseInspected(out.Stdout))

	running := services[:0]
	for _, service := range services {
		if service.State == "running" {
			running = append(running, service)
		}
	}
	if len(running) == 0 {
		t.Fatal("the demo project has no running containers")
	}
	return running
}

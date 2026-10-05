package compose

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Service is one entry from `docker compose ps --format json`.
//
// Unknown fields are ignored so newer compose releases keep parsing; every
// field zero-defaults so older releases that omit some keep parsing too
// (both come free with encoding/json, including `Publishers: null`).
type Service struct {
	// Name is the container name, e.g. `myapp-db-1`.
	Name string `json:"Name"`
	// Service is the compose service name, e.g. `db`.
	Service string `json:"Service"`

	// ID is the container id, which is what its cgroup directory is named
	// after: the key that ties a row to the kernel's counters for it.
	ID string `json:"ID"`

	// Project is the compose project the container belongs to. It is what
	// scopes the daemon's event stream to this session's containers, and it
	// comes from `ps` rather than from the directory name because a project
	// can be named anything.
	Project string `json:"Project"`
	// State is `running`, `exited`, `restarting`, `paused`, `created`,
	// or `dead`.
	State string `json:"State"`
	// Health is `healthy`, `unhealthy`, `starting`; empty without a
	// healthcheck.
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
	// Status is human-readable, e.g. `Up 2 hours (healthy)`.
	Status     string      `json:"Status"`
	Publishers []Publisher `json:"Publishers"`

	// Restarts is how many times docker has restarted the container. It is
	// not part of `compose ps` output — ApplyInspected fills it from a
	// separate `docker inspect` — so it stays nil when that reading is
	// unavailable, which the view shows differently from a genuine zero.
	Restarts *int `json:"-"`

	// Pid is the container's main process, the one whose network namespace
	// carries its traffic counters. Zero for anything not running, and
	// filled by the same inspect as Restarts.
	Pid int `json:"-"`

	// HostNetwork is a container with `network_mode: host`. Its process's
	// network counters are the host's own interfaces, so they say nothing
	// about the container and are not reported as its traffic.
	HostNetwork bool `json:"-"`
}

// Inspected is what `docker inspect` adds to what `ps` already said.
type Inspected struct {
	Restarts    int
	Pid         int
	HostNetwork bool
}

// ApplyInspected attaches what inspect reported to the services it belongs
// to, matching on container name. It is best-effort: a container that
// disappeared between the two commands simply keeps what `ps` said about it,
// and a service with no entry keeps an unknown restart count rather than
// being reported as never restarted.
func ApplyInspected(services []Service, inspected map[string]Inspected) {
	for i := range services {
		found, ok := inspected[services[i].Name]
		if !ok {
			continue
		}
		restarts := found.Restarts
		services[i].Restarts = &restarts
		services[i].Pid = found.Pid
		services[i].HostNetwork = found.HostNetwork
	}
}

// RestartsText renders the restart count for the table, `-` while it is
// unknown — the same placeholder the other optional columns use.
func (s *Service) RestartsText() string {
	if s.Restarts == nil {
		return "-"
	}
	return strconv.Itoa(*s.Restarts)
}

// Publisher is a port mapping from the `Publishers` array.
type Publisher struct {
	URL        string `json:"URL"`
	TargetPort uint16 `json:"TargetPort"`
	// PublishedPort is 0 when the port is exposed but not published on the
	// host.
	PublishedPort uint16 `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

// PortsSummary is a compact port list for display, e.g.
// `8080->80/tcp, 5432/tcp`. It collapses the IPv4/IPv6 duplicates compose
// emits per mapping.
func (s *Service) PortsSummary() string {
	var parts []string
	for _, p := range s.Publishers {
		var part string
		if p.PublishedPort != 0 {
			part = fmt.Sprintf("%d->%d/%s", p.PublishedPort, p.TargetPort, p.Protocol)
		} else {
			part = fmt.Sprintf("%d/%s", p.TargetPort, p.Protocol)
		}
		if !slices.Contains(parts, part) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

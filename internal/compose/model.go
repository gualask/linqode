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
	// not part of `compose ps` output — ApplyRestarts fills it from a
	// separate `docker inspect` — so it stays nil when that reading is
	// unavailable, which the view shows differently from a genuine zero.
	Restarts *int `json:"-"`
}

// ApplyRestarts attaches restart counts to the services they belong to,
// matching on container name. A service with no entry keeps an unknown
// count rather than being reported as never restarted.
func ApplyRestarts(services []Service, counts map[string]int) {
	for i := range services {
		if n, ok := counts[services[i].Name]; ok {
			services[i].Restarts = &n
		}
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

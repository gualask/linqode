package compose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ParsePS parses the stdout of `docker compose ps --all --format json`.
//
// It accepts both output shapes: NDJSON (one object per line, compose >=
// 2.21) and a single JSON array (older releases). Services are sorted by
// service name so the view is stable across refreshes.
func ParsePS(raw []byte) ([]Service, error) {
	trimmed := bytes.TrimSpace(raw)
	var services []Service
	switch {
	case len(trimmed) == 0:
	case trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &services); err != nil {
			return nil, fmt.Errorf("unexpected compose ps output: %w", err)
		}
	default:
		for line := range strings.Lines(string(trimmed)) {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var service Service
			if err := json.Unmarshal([]byte(line), &service); err != nil {
				return nil, fmt.Errorf("unexpected compose ps output: %w", err)
			}
			services = append(services, service)
		}
	}
	slices.SortFunc(services, func(a, b Service) int {
		if c := strings.Compare(a.Service, b.Service); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return services, nil
}

// ParseInspected parses the output of InspectCommand into what it adds to
// `ps`, keyed by container name as `compose ps` reports it — docker inspect
// prints the name with a leading slash, which is stripped here.
//
// It returns no error by design: a container that vanished between `ps` and
// `inspect` makes the command fail while the lines for the others are still
// good, so an unparseable line is skipped rather than discarding a whole
// reading that is mostly usable.
func ParseInspected(raw []byte) map[string]Inspected {
	found := make(map[string]Inspected)
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		restarts, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		entry := Inspected{Restarts: restarts}
		if len(fields) > 2 {
			// A container that is not running has no process; docker
			// reports 0, which is what it stays.
			entry.Pid, _ = strconv.Atoi(fields[2])
		}
		entry.HostNetwork = len(fields) > 3 && fields[3] == "host"
		found[strings.TrimPrefix(fields[0], "/")] = entry
	}
	return found
}

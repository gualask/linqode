package compose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
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

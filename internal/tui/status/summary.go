package status

// The project summary that rides on the title line, beside the target.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gualask/linqode/internal/tui/theme"
)

// Summary counts the services by state, with anything unhealthy
// called out: on a long table that one line is what says whether the
// project is in trouble. It rides on the title line, where there is room
// to spare — the footer's hints already compete for every column they get.
func (m *Model) Summary() string {
	if len(m.services) == 0 {
		return ""
	}
	states := map[string]int{}
	unhealthy := 0
	for _, s := range m.services {
		states[s.State]++
		if s.Health == "unhealthy" {
			unhealthy++
		}
	}
	names := make([]string, 0, len(states))
	for state := range states {
		names = append(names, state)
	}
	// Lifecycle order, not alphabetical: "4 running · 1 exited" is how an
	// operator reads a project, and states docker may add later still get a
	// stable place at the end.
	sort.SliceStable(names, func(i, j int) bool {
		ri, rj := stateRank(names[i]), stateRank(names[j])
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})

	parts := make([]string, 0, len(names)+1)
	for _, state := range names {
		parts = append(parts, stateStyle(state).Render(fmt.Sprintf("%d %s", states[state], state)))
	}
	if unhealthy > 0 {
		parts = append(parts, theme.Red.Render(fmt.Sprintf("%d unhealthy", unhealthy)))
	}
	return strings.Join(parts, theme.Dim.Render(" · "))
}

// stateRank orders the states the summary can hold; anything unknown sorts
// after them.
func stateRank(state string) int {
	for i, known := range []string{"running", "restarting", "paused", "created", "exited", "dead"} {
		if state == known {
			return i
		}
	}
	return 100
}

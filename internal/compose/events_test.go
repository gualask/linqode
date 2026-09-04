package compose

// Tests for the daemon's event stream. The sample lines are real output,
// captured from `docker events` against a compose project with a health
// check — including the shapes that are easy to get wrong: an action with a
// detail appended, and an exit code that is sometimes there and sometimes not.

import (
	"strings"
	"testing"
)

func TestEventsCommandScopesToTheProject(t *testing.T) {
	command := EventsCommand("myapp")
	if !strings.Contains(command, "'label=com.docker.compose.project=myapp'") {
		t.Errorf("the stream is not scoped to the project: %s", command)
	}
	if !strings.Contains(command, "--filter type=container") {
		t.Errorf("the stream is not limited to containers: %s", command)
	}
	// The health-check noise is what makes an unfiltered stream cost more
	// than the polling it replaces, so every action asked for is explicit.
	for _, action := range []string{"start", "die", "health_status"} {
		if !strings.Contains(command, "'event="+action+"'") {
			t.Errorf("%q is not among the watched actions: %s", action, command)
		}
	}
	if strings.Contains(command, "exec_") {
		t.Errorf("the stream asked for exec events: %s", command)
	}
}

// Without a project there is nothing to scope the stream to, and an
// unscoped `docker events` reports every container on the host.
func TestEventsCommandNeedsAProject(t *testing.T) {
	if command := EventsCommand(""); command != "" {
		t.Errorf("EventsCommand(\"\") = %q, want no command", command)
	}
}

func TestParseEvent(t *testing.T) {
	cases := []struct {
		line   string
		want   Event
		kind   string
		detail string
	}{
		{line: "die|evtest-hc-1|137",
			want: Event{Action: "die", Container: "evtest-hc-1", ExitCode: "137"},
			kind: "die"},
		{line: "start|evtest-hc-1|",
			want: Event{Action: "start", Container: "evtest-hc-1"},
			kind: "start"},
		{line: "health_status: healthy|evtest-hc-1|",
			want:   Event{Action: "health_status: healthy", Container: "evtest-hc-1"},
			kind:   "health_status",
			detail: "healthy"},
	}
	for _, test := range cases {
		got, ok := ParseEvent(test.line)
		if !ok || got != test.want {
			t.Errorf("ParseEvent(%q) = %+v, %v; want %+v", test.line, got, ok, test.want)
		}
		if got.Kind() != test.kind {
			t.Errorf("%q: kind = %q, want %q", test.line, got.Kind(), test.kind)
		}
		if got.Detail() != test.detail {
			t.Errorf("%q: detail = %q, want %q", test.line, got.Detail(), test.detail)
		}
	}
}

// The daemon writes warnings to the same stream; a line that is not an event
// must not become one with empty fields.
func TestParseEventRejectsWhatIsNotAnEvent(t *testing.T) {
	for _, line := range []string{"", "   ", "no separator here", "|missing-action", "die|"} {
		if event, ok := ParseEvent(line); ok {
			t.Errorf("ParseEvent(%q) accepted it as %+v", line, event)
		}
	}
}

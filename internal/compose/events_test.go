package compose

// Tests for the daemon's event stream. The sample lines are real output,
// captured from `docker events` against a compose project with a health
// check — including the shapes that are easy to get wrong: an action with a
// detail appended, and an exit code that is sometimes there and sometimes not.

import (
	"strings"
	"testing"
	"time"
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
	const restartedAt = 1788593169
	cases := []struct {
		line   string
		want   Event
		kind   string
		detail string
	}{
		{line: "1788593169|die|demo-web-1|137",
			want: Event{At: time.Unix(restartedAt, 0), Action: "die",
				Container: "demo-web-1", ExitCode: "137"},
			kind: "die"},
		{line: "1788593169|start|demo-web-1|",
			want: Event{At: time.Unix(restartedAt, 0), Action: "start",
				Container: "demo-web-1"},
			kind: "start"},
		{line: "1788593170|health_status: healthy|demo-web-1|",
			want: Event{At: time.Unix(restartedAt+1, 0),
				Action: "health_status: healthy", Container: "demo-web-1"},
			kind:   "health_status",
			detail: "healthy"},
		// A daemon that did not give a time still reported an event; the
		// caller fills the gap with when it read the line.
		{line: "|start|demo-web-1|",
			want: Event{Action: "start", Container: "demo-web-1"},
			kind: "start"},
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
	for _, line := range []string{"", "   ", "no separator here",
		"1788593169||demo-web-1|", "1788593169|die|", "die|demo-web-1|137"} {
		if event, ok := ParseEvent(line); ok {
			t.Errorf("ParseEvent(%q) accepted it as %+v", line, event)
		}
	}
}

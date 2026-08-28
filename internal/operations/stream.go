package operations

import (
	"context"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/remote"
)

const streamBuffer = 4_096

type EventKind string

const (
	EventStdout EventKind = "stdout"
	EventStderr EventKind = "stderr"
	EventLog    EventKind = "log"
	EventStats  EventKind = "stats"
	EventExit   EventKind = "exit"
)

// Event is one presentation-neutral line, typed stats sample, or terminal
// exit report from a remote stream.
type Event struct {
	Kind     EventKind
	Text     string
	Stats    compose.ContainerStats
	ExitCode int
}

// Feed is the consumer end of a remote stream. Stop is idempotent and closes
// the stream through context cancellation.
type Feed struct {
	Events <-chan Event
	Stop   func()
}

type lineMapper func(string) (Event, bool)

type streamPump struct {
	ctx          context.Context
	remoteEvents <-chan remote.ExecEvent
	events       chan Event
	stdout       lineMapper
	outLines     logs.LineAssembler
	errLines     logs.LineAssembler
	exitCode     int
}

func stdoutLine(line string) (Event, bool) {
	return Event{Kind: EventStdout, Text: line}, true
}

func stderrLine(line string) (Event, bool) {
	return Event{Kind: EventStderr, Text: line}, true
}

func logLine(line string) (Event, bool) {
	return Event{Kind: EventLog, Text: line}, true
}

func statsLine(line string) (Event, bool) {
	stats, ok := compose.ParseStats(line)
	return Event{Kind: EventStats, Stats: stats}, ok
}

func (o *HostOperator) startFeed(ctx context.Context, command string, stdout lineMapper) (Feed, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	remoteEvents, err := o.executor.ExecStream(streamCtx, command)
	if err != nil {
		cancel()
		return Feed{}, err
	}

	events := make(chan Event, streamBuffer)
	pump := &streamPump{
		ctx: streamCtx, remoteEvents: remoteEvents, events: events,
		stdout: stdout, exitCode: -1,
	}
	go pump.run()
	return Feed{Events: events, Stop: cancel}, nil
}

func (p *streamPump) run() {
	defer close(p.events)
	for event := range p.remoteEvents {
		if !p.apply(event) {
			return
		}
	}
	p.finish()
}

func (p *streamPump) apply(event remote.ExecEvent) bool {
	switch event.Kind {
	case remote.ExecStdout:
		return p.emitLines(p.outLines.Push(event.Data), p.stdout)
	case remote.ExecStderr:
		return p.emitLines(p.errLines.Push(event.Data), stderrLine)
	case remote.ExecExit:
		p.exitCode = event.ExitCode
	}
	return true
}

func (p *streamPump) finish() {
	if line, ok := p.outLines.Finish(); ok && !p.emit(line, p.stdout) {
		return
	}
	if line, ok := p.errLines.Finish(); ok && !p.emit(line, stderrLine) {
		return
	}
	p.send(Event{Kind: EventExit, ExitCode: p.exitCode})
}

func (p *streamPump) emitLines(lines []string, mapper lineMapper) bool {
	for _, line := range lines {
		if !p.emit(line, mapper) {
			return false
		}
	}
	return true
}

func (p *streamPump) emit(line string, mapper lineMapper) bool {
	event, ok := mapper(line)
	return !ok || p.send(event)
}

func (p *streamPump) send(event Event) bool {
	select {
	case p.events <- event:
		return true
	case <-p.ctx.Done():
		return false
	}
}

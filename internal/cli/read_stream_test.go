package cli

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/operations"
)

func TestRunReadLogsDoesNotBufferCompleteStream(t *testing.T) {
	events := make(chan operations.Event)
	observer := fakeObserver{logs: func(context.Context, string, int, bool) (operations.Feed, error) {
		return operations.Feed{Events: events, Stop: func() {}}, nil
	}}
	stdout := &notifyingBuffer{written: make(chan struct{})}
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- RunRead(context.Background(), Invocation{
			Command: CommandLogs, Host: "production", Service: "web", Tail: 200, Follow: true,
		}, observer, stdout, &stderr)
	}()

	events <- operations.Event{Kind: operations.EventLog, Text: "available now"}
	select {
	case <-stdout.written:
	case <-time.After(time.Second):
		t.Fatal("first JSONL event was buffered until stream completion")
	}
	events <- operations.Event{Kind: operations.EventExit, ExitCode: 0}
	close(events)
	select {
	case code := <-done:
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not finish")
	}
}

type notifyingBuffer struct {
	bytes.Buffer
	once    sync.Once
	written chan struct{}
}

func (w *notifyingBuffer) Write(raw []byte) (int, error) {
	n, err := w.Buffer.Write(raw)
	w.once.Do(func() { close(w.written) })
	return n, err
}

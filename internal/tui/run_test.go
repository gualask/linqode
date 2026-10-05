package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/operations"
)

// The program runs on the run's context: cancelling it ends the screen and
// Run reports the context's error. The streams it opened were opened on that
// same context, so cancelling it is what stops them.
func TestACancelledContextEndsTheProgram(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan struct{})
	backend := Backend{
		Services: func() ([]compose.Service, error) {
			return []compose.Service{{Service: "web", Name: "app-web-1", Project: "app"}}, nil
		},
		Watch: func(string) (operations.Feed, error) {
			defer close(opened)
			return operations.Feed{Events: make(chan operations.Event)}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Info{Target: "deploy@prod"}, backend,
			tea.WithInput(&bytes.Buffer{}), tea.WithOutput(io.Discard))
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the program never opened the stream")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want the context's error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not end the program")
	}
}

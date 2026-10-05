package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

// pipeTerminal is a terminal whose keyboard the test types on.
func pipeTerminal(t *testing.T) (*term.Terminal, *io.PipeWriter) {
	t.Helper()
	keys, typing := io.Pipe()
	t.Cleanup(func() { typing.Close() })
	return term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{keys, io.Discard}, ""), typing
}

// within fails the test if call has not returned in time: a prompt that
// ignores Ctrl-C or the run's cancellation hangs here instead.
func within[T any](t *testing.T, call func() T) T {
	t.Helper()
	result := make(chan T, 1)
	go func() { result <- call() }()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt never returned")
		panic("unreachable")
	}
}

type answer struct {
	text string
	err  error
}

func TestPassphraseIsTheLineTyped(t *testing.T) {
	terminal, typing := pipeTerminal(t)
	go typing.Write([]byte("s3cret\r"))

	got := within(t, func() answer {
		text, err := readPassphrase(context.Background(), terminal, "passphrase: ")
		return answer{text, err}
	})
	if got.err != nil || got.text != "s3cret" {
		t.Fatalf("got %q, %v", got.text, got.err)
	}
}

// The terminal is raw while the passphrase is typed, so Ctrl-C arrives as a
// key rather than as SIGINT, and it aborts rather than waiting for Enter.
func TestCtrlCAtThePassphraseAborts(t *testing.T) {
	terminal, typing := pipeTerminal(t)
	go typing.Write([]byte("half\x03"))

	got := within(t, func() answer {
		text, err := readPassphrase(context.Background(), terminal, "passphrase: ")
		return answer{text, err}
	})
	if !errors.Is(got.err, errInterrupted) || got.text != "" {
		t.Fatalf("got %q, %v; want errInterrupted", got.text, got.err)
	}
}

// SIGTERM or SIGHUP cancel the run while a prompt waits on a keyboard that
// will never be typed on again.
func TestACancelledRunAbandonsThePrompts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	terminal, _ := pipeTerminal(t)
	got := within(t, func() answer {
		text, err := readPassphrase(ctx, terminal, "passphrase: ")
		return answer{text, err}
	})
	if !errors.Is(got.err, errInterrupted) {
		t.Errorf("passphrase: got %q, %v; want errInterrupted", got.text, got.err)
	}

	keys, typing := io.Pipe()
	defer typing.Close()
	var shown strings.Builder
	got = within(t, func() answer {
		_, err := confirmHostKey(ctx, keys, &shown, "example.com", 22, "ssh-ed25519", "SHA256:x")
		return answer{err: err}
	})
	if !errors.Is(got.err, errInterrupted) {
		t.Errorf("host key: got %v; want errInterrupted", got.err)
	}
}

func TestHostKeyConfirmationReadsAnswers(t *testing.T) {
	for input, want := range map[string]bool{"yes\n": true, "maybe\nno\n": false, "": false} {
		var shown strings.Builder
		accepted, err := confirmHostKey(context.Background(), strings.NewReader(input), &shown,
			"example.com", 22, "ssh-ed25519", "SHA256:x")
		if err != nil || accepted != want {
			t.Errorf("%q: got %v, %v; want %v", input, accepted, err, want)
		}
		if !strings.Contains(shown.String(), "SHA256:x") {
			t.Errorf("%q: the fingerprint was not shown: %q", input, shown.String())
		}
	}
}

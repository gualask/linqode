package main

// Terminal implementations of the connect-phase prompts. These run before
// the TUI takes over the screen, so they use plain stderr/stdin, mirroring
// the OpenSSH user experience.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// errInterrupted is a prompt the operator walked away from: Ctrl-C at it,
// or a signal that cancelled the run while it waited.
var errInterrupted = errors.New("interrupted")

// terminalPrompter asks on the terminal. ctx is the run's: a prompt is a
// wait on the keyboard, and a run cancelled during it must not stay parked
// there until Enter.
type terminalPrompter struct {
	ctx context.Context
}

func (p terminalPrompter) ConfirmHostKey(host string, port uint16, algorithm, fingerprint string) (bool, error) {
	return confirmHostKey(p.ctx, os.Stdin, os.Stderr, host, port, algorithm, fingerprint)
}

// confirmHostKey reads the answer in cooked mode, where Ctrl-C is the
// terminal's SIGINT and so arrives as ctx being cancelled.
func confirmHostKey(ctx context.Context, in io.Reader, out io.Writer,
	host string, port uint16, algorithm, fingerprint string) (bool, error) {
	fmt.Fprintf(out,
		"The authenticity of host '%s:%d' can't be established.\n%s key fingerprint is %s.\n",
		host, port, algorithm, fingerprint)
	stdin := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "Are you sure you want to continue connecting (yes/no)? ")
		answer, err := awaitInput(ctx, func() (string, error) { return stdin.ReadString('\n') })
		if errors.Is(err, errInterrupted) {
			fmt.Fprintln(out)
			return false, err
		}
		if err != nil {
			return false, nil // EOF on stdin: refuse
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "yes", "y":
			return true, nil
		case "no", "n":
			return false, nil
		default:
			fmt.Fprintln(out, "Please type 'yes' or 'no'.")
		}
	}
}

// AskPassphrase reads without echo. The terminal is put in raw mode here,
// on this goroutine, rather than inside the read: a read abandoned on
// cancel is still blocked when this returns, and the terminal has to be
// restored by the code that is still running, not by the code that is not.
// Raw mode also turns Ctrl-C into a key, which the reader answers at once —
// where with echo merely off it was a SIGINT the run's handler took, and the
// prompt went on waiting for Enter.
func (p terminalPrompter) AskPassphrase(path string) (string, error) {
	prompt := fmt.Sprintf("Enter passphrase for key '%s' (empty to skip): ", path)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		return "", nil // no terminal to ask on: skip the key
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stderr}, "")
	// The line editor wraps at the width it is told, 80 if told nothing; a
	// terminal reporting no size at all is told nothing, since wrapping at
	// zero puts every character of the prompt on a line of its own.
	if width, height, err := term.GetSize(int(os.Stderr.Fd())); err == nil && width > 0 {
		_ = terminal.SetSize(width, height)
	}
	secret, err := readPassphrase(p.ctx, terminal, prompt)
	_ = term.Restore(fd, state)
	if err != nil {
		fmt.Fprintln(os.Stderr) // Enter ended the line; nothing else did
	}
	return secret, err
}

// readPassphrase reads one line without echo. The terminal answers Ctrl-C,
// and Ctrl-D on an empty line, with io.EOF, which is the operator declining
// to go on.
func readPassphrase(ctx context.Context, terminal *term.Terminal, prompt string) (string, error) {
	return awaitInput(ctx, func() (string, error) {
		secret, err := terminal.ReadPassword(prompt)
		if errors.Is(err, io.EOF) {
			return "", errInterrupted
		}
		return secret, err
	})
}

// awaitInput runs read, which waits on the keyboard, and returns what it
// returns — unless ctx ends first, which is errInterrupted. The read is
// then left blocked: nothing can unblock a read on stdin, and the process is
// on its way out.
func awaitInput[T any](ctx context.Context, read func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := read()
		done <- result{value, err}
	}()
	select {
	case got := <-done:
		return got.value, got.err
	case <-ctx.Done():
		var zero T
		return zero, errInterrupted
	}
}

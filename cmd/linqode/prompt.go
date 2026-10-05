package main

// Terminal implementations of the connect-phase prompts. These run before
// the TUI takes over the screen, so they use plain stderr/stdin, mirroring
// the OpenSSH user experience.

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

type terminalPrompter struct{}

func (terminalPrompter) ConfirmHostKey(host string, port uint16, algorithm, fingerprint string) (bool, error) {
	fmt.Fprintf(os.Stderr,
		"The authenticity of host '%s:%d' can't be established.\n%s key fingerprint is %s.\n",
		host, port, algorithm, fingerprint)
	stdin := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, "Are you sure you want to continue connecting (yes/no)? ")
		answer, err := stdin.ReadString('\n')
		if err != nil {
			return false, nil // EOF on stdin: refuse
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "yes", "y":
			return true, nil
		case "no", "n":
			return false, nil
		default:
			fmt.Fprintln(os.Stderr, "Please type 'yes' or 'no'.")
		}
	}
}

func (terminalPrompter) AskPassphrase(path string) (string, error) {
	fmt.Fprintf(os.Stderr, "Enter passphrase for key '%s' (empty to skip): ", path)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", nil // no terminal to ask on: skip the key
	}
	secret, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(secret), nil
}

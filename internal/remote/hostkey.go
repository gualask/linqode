package remote

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyPolicy verifies server keys against a known_hosts file with the MVP
// policy: known key → accept; unknown host → TOFU prompt, persist on accept;
// changed key → refuse, never bypassable.
//
// x/crypto wraps callback errors inside its handshake error, so the policy
// also records the precise error for Connect to surface.
type hostKeyPolicy struct {
	target   Target
	file     string
	prompter Prompter
	// err is the precise verification error of the last handshake, when the
	// generic handshake error should be replaced.
	err error
}

func newHostKeyPolicy(target Target, file string, prompter Prompter) (*hostKeyPolicy, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, fmt.Errorf("cannot prepare known_hosts: %w", err)
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare known_hosts: %w", err)
	}
	f.Close()
	return &hostKeyPolicy{target: target, file: file, prompter: prompter}, nil
}

func (p *hostKeyPolicy) callback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	check, err := knownhosts.New(p.file)
	if err != nil {
		return err
	}
	err = check(hostname, remote, key)
	if err == nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return err // revoked key or a malformed file: refuse as-is
	}
	if len(keyErr.Want) > 0 {
		p.err = &HostKeyChangedError{
			Host: p.target.DisplayHost,
			Port: p.target.Port,
			Line: keyErr.Want[0].Line,
		}
		return p.err
	}

	// Unknown host: trust-on-first-use.
	accepted, err := p.prompter.ConfirmHostKey(
		p.target.DisplayHost, p.target.Port, key.Type(), ssh.FingerprintSHA256(key))
	if err != nil {
		p.err = err
		return err
	}
	if !accepted {
		p.err = &HostKeyRejectedError{Host: p.target.DisplayHost, Port: p.target.Port}
		return p.err
	}
	if err := p.learn(hostname, key); err != nil {
		p.err = err
		return err
	}
	return nil
}

// learn appends the accepted key to the known_hosts file.
func (p *hostKeyPolicy) learn(hostname string, key ssh.PublicKey) error {
	f, err := os.OpenFile(p.file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot persist host key: %w", err)
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if _, err := fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("cannot persist host key: %w", err)
	}
	return nil
}

// defaultKnownHostsFile is ~/.ssh/known_hosts.
func defaultKnownHostsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate known_hosts: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

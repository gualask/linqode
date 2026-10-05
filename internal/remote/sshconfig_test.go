package remote_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/agent"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/remote"
)

// With IdentitiesOnly the agent may sign only for a configured identity:
// an authorized key it holds for something else is never offered.
func TestIdentitiesOnlyKeepsUnconfiguredAgentKeysBack(t *testing.T) {
	server := spawn(t, nil)
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: server.clientKey}); err != nil {
		t.Fatal(err)
	}
	fakeAgent(t, keyring)
	other, _ := genKeyPair(t)
	configured := filepath.Join(server.dir, "id_configured")
	writeKey(t, configured, other, "")

	target := server.target()
	target.IdentityFiles = []string{configured}
	target.IdentitiesOnly = true
	opts := server.options()
	opts.IdentitiesOnly = false
	_, err := remote.ConnectWith(testContext(t), target, &prompter{acceptHostKey: true}, opts)
	var authErr *remote.AuthFailedError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want AuthFailedError: the agent's unconfigured key was offered", err)
	}
}

// The agent still signs for a configured identity, recognised by the public
// half an encrypted key file carries, so no passphrase is asked for.
func TestIdentitiesOnlyUsesAgentForConfiguredKey(t *testing.T) {
	server := spawn(t, nil)
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: server.clientKey}); err != nil {
		t.Fatal(err)
	}
	fakeAgent(t, keyring)
	writeKey(t, server.keyFile, server.clientKey, "secret")

	target := server.target()
	target.IdentitiesOnly = true
	opts := server.options()
	opts.IdentitiesOnly = false
	p := &prompter{acceptHostKey: true}
	session, err := remote.ConnectWith(testContext(t), target, p, opts)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	if got := p.passPrompts.Load(); got != 0 {
		t.Errorf("asked for a passphrase %d times although the agent holds the key", got)
	}
}

// Every UserKnownHostsFile is consulted, and TOFU writes to the first.
func TestUserKnownHostsFiles(t *testing.T) {
	server := spawn(t, nil)
	second := filepath.Join(server.dir, "known_hosts2")
	server.pin(t, server.hostKey.PublicKey())
	if err := os.Rename(server.knownHosts, second); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(server.dir, "sub", "known_hosts1")
	target := server.target()
	target.KnownHostsFiles = []string{first, second}
	opts := remote.ConnectOptions{IdentitiesOnly: true}
	session, err := remote.ConnectWith(testContext(t), target, cli.NonInteractivePrompter{}, opts)
	if err != nil {
		t.Fatalf("key pinned in the second file was not found: %v", err)
	}
	session.Close()

	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	session, err = remote.ConnectWith(testContext(t), target, &prompter{acceptHostKey: true}, opts)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	raw, err := os.ReadFile(first)
	if err != nil || !strings.Contains(string(raw), "ssh-ed25519") {
		t.Fatalf("TOFU did not write the first file: %q, %v", raw, err)
	}
}

// The global known_hosts vouches for a host and is never written.
func TestGlobalKnownHostsFileIsReadOnly(t *testing.T) {
	server := spawn(t, nil)
	server.pin(t, server.hostKey.PublicKey())
	global := filepath.Join(server.dir, "ssh_known_hosts")
	if err := os.Rename(server.knownHosts, global); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	target := server.target()
	target.GlobalKnownHostsFiles = []string{global, filepath.Join(server.dir, "missing")}
	session, err := remote.ConnectWith(testContext(t), target, cli.NonInteractivePrompter{}, server.options())
	if err != nil {
		t.Fatalf("key pinned in the global file was not found: %v", err)
	}
	session.Close()
	after, err := os.ReadFile(global)
	if err != nil || string(after) != string(before) {
		t.Fatalf("global known_hosts changed: %q, %v", after, err)
	}
}

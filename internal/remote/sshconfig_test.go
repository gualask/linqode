package remote_test

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh/agent"

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

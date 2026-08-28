package remote_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/remote"
)

func TestNonInteractiveUnknownHostKeyFailsWithoutLearning(t *testing.T) {
	server := spawn(t, nil)
	_, err := remote.ConnectWith(testContext(t), server.target(), cli.NonInteractivePrompter{}, server.options())
	var unknown *remote.UnknownHostKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("got %v, want UnknownHostKeyError", err)
	}
	if unknown.Algorithm == "" || unknown.Fingerprint == "" {
		t.Fatalf("unknown host-key details = %+v", unknown)
	}
	raw, readErr := os.ReadFile(server.knownHosts)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(raw) != 0 {
		t.Fatalf("non-interactive connection learned host key: %q", raw)
	}
}

func TestChangedHostKeyRefusesWithoutPrompting(t *testing.T) {
	server := spawn(t, nil)
	otherKey := genSigner(t)
	addr := knownhosts.Normalize("127.0.0.1:" + strconv.Itoa(int(server.port)))
	line := knownhosts.Line([]string{addr}, otherKey.PublicKey())
	if err := os.WriteFile(server.knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &prompter{acceptHostKey: true}
	_, err := remote.ConnectWith(testContext(t), server.target(), p, server.options())
	var changed *remote.HostKeyChangedError
	if !errors.As(err, &changed) || changed.Line != 1 {
		t.Fatalf("got %+v, want HostKeyChangedError at line 1", err)
	}
	if got := p.hostKeyPrompts.Load(); got != 0 {
		t.Errorf("prompter consulted %d times on a changed key", got)
	}
}

func TestUnauthorizedKeyFailsAuth(t *testing.T) {
	server := spawn(t, nil)
	key, _ := genKeyPair(t)
	unauthorized := filepath.Join(server.dir, "id_other")
	writeKey(t, unauthorized, key, "")
	target := server.target()
	target.IdentityFiles = []string{unauthorized}
	_, err := remote.ConnectWith(testContext(t), target, &prompter{acceptHostKey: true}, server.options())
	var authErr *remote.AuthFailedError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want AuthFailedError", err)
	}
}

func TestConnectCancellationInterruptsSSHHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	go func() {
		_, connectErr := remote.ConnectWith(ctx, remote.Target{
			Host: "127.0.0.1", DisplayHost: "127.0.0.1", Port: uint16(address.Port), User: "test",
		}, &prompter{}, remote.ConnectOptions{KnownHostsFile: knownHosts, IdentitiesOnly: true})
		done <- connectErr
	}()
	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case <-time.After(guardTimeout):
		cancel()
		t.Fatal("client did not reach handshake")
	}
	defer serverConn.Close()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ConnectWith() error = %v, want context.Canceled", err)
		}
	case <-time.After(guardTimeout):
		t.Fatal("SSH handshake did not stop after cancellation")
	}
}

func TestEncryptedKeyAsksPassphrase(t *testing.T) {
	server := spawn(t, map[string]script{"true": {}})
	writeKey(t, server.keyFile, server.clientKey, "sesame")
	p := &prompter{acceptHostKey: true, passphrase: "sesame"}
	session := connect(t, server, p)
	if _, err := session.Exec(testContext(t), "true"); err != nil {
		t.Fatal(err)
	}
	if got := p.passPrompts.Load(); got != 1 {
		t.Errorf("asked for the passphrase %d times, want 1", got)
	}
	wrong := &prompter{acceptHostKey: true, passphrase: "nope"}
	_, err := remote.ConnectWith(testContext(t), server.target(), wrong, server.options())
	var bad *remote.BadPassphraseError
	if !errors.As(err, &bad) || wrong.passPrompts.Load() != 3 {
		t.Fatalf("wrong-passphrase result = %v, prompts = %d", err, wrong.passPrompts.Load())
	}
	_, err = remote.ConnectWith(testContext(t), server.target(), cli.NonInteractivePrompter{}, server.options())
	var required *remote.PassphraseRequiredError
	if !errors.As(err, &required) || required.Path != server.keyFile {
		t.Fatalf("got %v, want PassphraseRequiredError for %s", err, server.keyFile)
	}
}

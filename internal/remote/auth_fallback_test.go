package remote_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/agent"

	"github.com/gualask/linqode/internal/remote"
)

func TestAuthFallsBackToLaterIdentity(t *testing.T) {
	for _, first := range []string{"missing", "invalid", "unauthorized", "encrypted skipped"} {
		t.Run(first, func(t *testing.T) {
			server := spawn(t, nil)
			path := filepath.Join(server.dir, "first")
			other, _ := genKeyPair(t)
			switch first {
			case "invalid":
				if err := os.WriteFile(path, []byte("invalid private key"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unauthorized":
				writeKey(t, path, other, "")
			case "encrypted skipped":
				writeKey(t, path, other, "secret")
			}
			target := server.target()
			target.IdentityFiles = []string{path, server.keyFile}
			session, err := remote.ConnectWith(testContext(t), target, &prompter{acceptHostKey: true}, server.options())
			if err != nil {
				t.Fatalf("valid later identity was not accepted: %v", err)
			}
			session.Close()
		})
	}
}

func TestAuthDoesNotUnlockIdentityAfterSuccess(t *testing.T) {
	server := spawn(t, nil)
	other, _ := genKeyPair(t)
	path := filepath.Join(server.dir, "encrypted")
	writeKey(t, path, other, "secret")
	target := server.target()
	target.IdentityFiles = append(target.IdentityFiles, path)
	p := &prompter{acceptHostKey: true}
	session, err := remote.ConnectWith(testContext(t), target, p, server.options())
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	if got := p.passPrompts.Load(); got != 0 {
		t.Fatalf("prompted %d times after an earlier key succeeded", got)
	}
}

func TestAuthAgentFallbackAndLazyPassphrase(t *testing.T) {
	for _, identity := range []string{"empty", "unauthorized", "authorized"} {
		t.Run(identity, func(t *testing.T) {
			server := spawn(t, nil)
			keyring := agent.NewKeyring()
			if identity != "empty" {
				key := server.clientKey
				if identity == "unauthorized" {
					key, _ = genKeyPair(t)
				}
				if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
					t.Fatal(err)
				}
			}
			fakeAgent(t, keyring)
			writeKey(t, server.keyFile, server.clientKey, "secret")
			opts := server.options()
			opts.IdentitiesOnly = false
			p := &prompter{acceptHostKey: true, passphrase: "secret"}
			session, err := remote.ConnectWith(testContext(t), server.target(), p, opts)
			if err != nil {
				t.Fatal(err)
			}
			session.Close()
			wantPrompts := int32(1)
			if identity == "authorized" {
				wantPrompts = 0
			}
			if got := p.passPrompts.Load(); got != wantPrompts {
				t.Fatalf("passphrase prompts = %d, want %d", got, wantPrompts)
			}
		})
	}
}

func fakeAgent(t *testing.T, keyring agent.Agent) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("SSH agent fixture uses a Unix socket")
	}
	// t.TempDir includes the test name, exceeding macOS's socket path limit.
	dir, err := os.MkdirTemp("", "lq-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(guardTimeout))
		_ = agent.ServeAgent(keyring, conn)
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
}

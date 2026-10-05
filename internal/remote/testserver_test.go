package remote_test

// The fixture: a scripted SSH server on an ephemeral loopback port, so the
// production client is exercised for real — handshake, host-key policy,
// auth, exec, streaming — without any external dependency. The auth model
// accepts exactly one key pair for user "linqode-test"; anything else is
// rejected.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	gliderssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"

	"github.com/gualask/linqode/internal/remote"
)

// guardTimeout bounds every potentially-blocking wait so a regression hangs
// the test, not CI.
const guardTimeout = 10 * time.Second

const testUser = "linqode-test"

// script maps an exact command to its reply. holdOpen emulates a follower
// (logs -f) that only ends when the client cancels.
type script struct {
	stdout   []string
	stderr   []string
	exit     int
	holdOpen bool
}

type testServer struct {
	port       uint16
	hostKey    ssh.Signer // the first of the keys the server offers
	dir        string
	keyFile    string // authorized client identity (private key)
	clientKey  ed25519.PrivateKey
	knownHosts string
}

// spawn starts a scripted server for this test; it is torn down with the
// test.
func spawn(t *testing.T, scripts map[string]script) *testServer {
	t.Helper()
	return spawnWithHostKeys(t, scripts, genSigner(t))
}

// spawnWithHostKeys is spawn with the server offering every given host key,
// so a test can choose which of them the client already knows.
func spawnWithHostKeys(t *testing.T, scripts map[string]script, hostKeys ...ssh.Signer) *testServer {
	t.Helper()
	dir := t.TempDir()

	hostKey := hostKeys[0]
	clientKey, clientSigner := genKeyPair(t)
	keyFile := filepath.Join(dir, "id_ed25519")
	writeKey(t, keyFile, clientKey, "")
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	authorized := clientSigner.PublicKey()
	server := &gliderssh.Server{
		PublicKeyHandler: func(ctx gliderssh.Context, key gliderssh.PublicKey) bool {
			return ctx.User() == testUser && gliderssh.KeysEqual(key, authorized)
		},
		Handler: func(s gliderssh.Session) {
			sc, ok := scripts[s.RawCommand()]
			if !ok {
				fmt.Fprintf(s.Stderr(), "unscripted command %q\n", s.RawCommand())
				s.Exit(127)
				return
			}
			for _, chunk := range sc.stdout {
				fmt.Fprint(s, chunk)
			}
			for _, chunk := range sc.stderr {
				fmt.Fprint(s.Stderr(), chunk)
			}
			if sc.holdOpen {
				<-s.Context().Done()
				return
			}
			s.Exit(sc.exit)
		},
	}
	for _, key := range hostKeys {
		server.AddHostKey(key)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	return &testServer{
		port:       uint16(listener.Addr().(*net.TCPAddr).Port),
		hostKey:    hostKey,
		dir:        dir,
		keyFile:    keyFile,
		clientKey:  clientKey,
		knownHosts: knownHosts,
	}
}

// target returns a resolved Target pointing at the fixture with the
// authorized identity.
func (s *testServer) target() remote.Target {
	return remote.Target{
		Host:          "127.0.0.1",
		DisplayHost:   "127.0.0.1",
		Port:          s.port,
		User:          testUser,
		IdentityFiles: []string{s.keyFile},
	}
}

// options returns hermetic ConnectOptions: a per-test known_hosts file and
// no agent, so the developer's real ~/.ssh and agent keys are never
// involved.
func (s *testServer) options() remote.ConnectOptions {
	return remote.ConnectOptions{KnownHostsFile: s.knownHosts, IdentitiesOnly: true}
}

func genKeyPair(t *testing.T) (ed25519.PrivateKey, ssh.Signer) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, signer
}

func genSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, signer := genKeyPair(t)
	return signer
}

// writeKey writes a private key in OpenSSH PEM format, encrypted when a
// passphrase is given.
func writeKey(t *testing.T, path string, key ed25519.PrivateKey, passphrase string) {
	t.Helper()
	var block *pem.Block
	var err error
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(key, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

// prompter is a scripted Prompter that counts how often it is consulted.
// The zero value refuses host keys and skips passphrases.
type prompter struct {
	acceptHostKey  bool
	passphrase     string
	hostKeyPrompts atomic.Int32
	passPrompts    atomic.Int32
}

func (p *prompter) ConfirmHostKey(host string, port uint16, algorithm, fingerprint string) (bool, error) {
	p.hostKeyPrompts.Add(1)
	return p.acceptHostKey, nil
}

func (p *prompter) AskPassphrase(path string) (string, error) {
	p.passPrompts.Add(1)
	return p.passphrase, nil
}

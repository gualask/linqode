package remote_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/gualask/linqode/internal/cli"
	"github.com/gualask/linqode/internal/remote"
)

func genECDSASigner(t *testing.T) ssh.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// pin writes known_hosts lines for the fixture's address, one per key.
func (s *testServer) pin(t *testing.T, keys ...ssh.PublicKey) {
	t.Helper()
	addr := knownhosts.Normalize("127.0.0.1:" + strconv.Itoa(int(s.port)))
	var raw []byte
	for _, key := range keys {
		raw = append(raw, knownhosts.Line([]string{addr}, key)+"\n"...)
	}
	if err := os.WriteFile(s.knownHosts, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A host that offers ECDSA beside the ed25519 key known_hosts records must
// be verified with the recorded key, not refused as changed: x/crypto
// prefers ECDSA, so without asking for the recorded types first the server
// shows a key nobody pinned.
func TestKnownKeyOfAnotherTypeIsNotAChangedKey(t *testing.T) {
	ed := genSigner(t)
	server := spawnWithHostKeys(t, map[string]script{"true": {}}, ed, genECDSASigner(t))
	server.pin(t, ed.PublicKey())
	session, err := remote.ConnectWith(testContext(t), server.target(), cli.NonInteractivePrompter{}, server.options())
	if err != nil {
		t.Fatalf("recorded ed25519 key was not used: %v", err)
	}
	session.Close()
}

// The restriction must not weaken the refusal: a recorded key of the type
// the server offers that differs is still a changed key.
func TestChangedKeyOfRecordedTypeIsRefusedAmongOthers(t *testing.T) {
	server := spawnWithHostKeys(t, nil, genSigner(t), genECDSASigner(t))
	server.pin(t, genSigner(t).PublicKey())
	p := &prompter{acceptHostKey: true}
	_, err := remote.ConnectWith(testContext(t), server.target(), p, server.options())
	var changed *remote.HostKeyChangedError
	if !errors.As(err, &changed) || changed.Line != 1 {
		t.Fatalf("got %v, want HostKeyChangedError at line 1", err)
	}
	if got := p.hostKeyPrompts.Load(); got != 0 {
		t.Errorf("prompter consulted %d times on a changed key", got)
	}
}

// A known_hosts kept hashed (HashKnownHosts) must not gain a line naming
// the host in clear.
func TestTOFUHashesWhenKnownHostsIsHashed(t *testing.T) {
	server := spawn(t, nil)
	other := knownhosts.Line([]string{knownhosts.HashHostname("elsewhere.example")}, genSigner(t).PublicKey())
	if err := os.WriteFile(server.knownHosts, []byte(other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := remote.ConnectWith(testContext(t), server.target(), &prompter{acceptHostKey: true}, server.options())
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	raw, err := os.ReadFile(server.knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "|1|") || strings.Contains(lines[1], "127.0.0.1") {
		t.Fatalf("learned line is not hashed: %q", raw)
	}
	silent := &prompter{}
	session, err = remote.ConnectWith(testContext(t), server.target(), silent, server.options())
	if err != nil {
		t.Fatalf("hashed entry was not recognised: %v", err)
	}
	session.Close()
}

// A server that cannot show any recorded type is refused, not learned: a new
// key type in place of the pinned one is what an impostor would offer.
func TestOnlyUnrecordedKeyTypeIsRefused(t *testing.T) {
	server := spawnWithHostKeys(t, nil, genECDSASigner(t))
	server.pin(t, genSigner(t).PublicKey())
	p := &prompter{acceptHostKey: true}
	_, err := remote.ConnectWith(testContext(t), server.target(), p, server.options())
	var notRecorded *remote.HostKeyTypeNotRecordedError
	if !errors.As(err, &notRecorded) {
		t.Fatalf("got %v, want HostKeyTypeNotRecordedError", err)
	}
	if got := p.hostKeyPrompts.Load(); got != 0 {
		t.Errorf("prompter consulted %d times with a key already pinned", got)
	}
}

// A @cert-authority line vouches for a host certificate, which therefore
// has to be among the algorithms offered.
func TestHostCertificateFromKnownAuthority(t *testing.T) {
	ca := genSigner(t)
	hostKey := genSigner(t)
	cert := &ssh.Certificate{
		Key:             hostKey.PublicKey(),
		CertType:        ssh.HostCert,
		ValidPrincipals: []string{"127.0.0.1"},
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	certSigner, err := ssh.NewCertSigner(cert, hostKey)
	if err != nil {
		t.Fatal(err)
	}
	server := spawnWithHostKeys(t, nil, certSigner)
	addr := knownhosts.Normalize("127.0.0.1:" + strconv.Itoa(int(server.port)))
	line := "@cert-authority " + addr + " " + string(ssh.MarshalAuthorizedKey(ca.PublicKey()))
	if err := os.WriteFile(server.knownHosts, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := remote.ConnectWith(testContext(t), server.target(), cli.NonInteractivePrompter{}, server.options())
	if err != nil {
		t.Fatalf("certificate from a known authority was refused: %v", err)
	}
	session.Close()
}

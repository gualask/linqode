package remote

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// failingPrompter fails the test if consulted.
type failingPrompter struct{ t *testing.T }

func (p failingPrompter) ConfirmHostKey(string, uint16, string, string) (bool, error) {
	p.t.Error("host key prompt on a host with pinned keys")
	return true, nil
}

func (p failingPrompter) AskPassphrase(string) (string, error) {
	p.t.Error("unexpected passphrase prompt")
	return "", nil
}

func publicKey(t *testing.T, private any) ssh.PublicKey {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

// When the algorithm restriction could not keep a key of another type out,
// the callback still refuses it rather than offering it for TOFU.
func TestUnrecordedKeyTypeIsNeverLearned(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{"example.com"}, publicKey(t, ed))
	if err := os.WriteFile(file, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := Target{Host: "example.com", DisplayHost: "example.com", Port: 22}
	policy, err := newHostKeyPolicy(target, "example.com:22", file, failingPrompter{t})
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.hostKeyAlgorithms("example.com:22"); !slices.Equal(got, []string{ssh.KeyAlgoED25519}) {
		t.Errorf("offered %v, want only ssh-ed25519", got)
	}
	err = policy.callback("example.com:22", &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}, publicKey(t, ec))
	var notRecorded *HostKeyTypeNotRecordedError
	if !errors.As(err, &notRecorded) || notRecorded.Offered != ssh.KeyAlgoECDSA256 {
		t.Fatalf("got %v, want HostKeyTypeNotRecordedError for the ECDSA key", err)
	}
}

func TestRecordedRSAKeyOffersEverySignature(t *testing.T) {
	policy := &hostKeyPolicy{recorded: []string{ssh.KeyAlgoRSA}, db: knownHostsDB{
		check: func(string, net.Addr, ssh.PublicKey) error { return &knownhosts.KeyError{} },
	}}
	want := []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	if got := policy.hostKeyAlgorithms("example.com:22"); !slices.Equal(got, want) {
		t.Errorf("offered %v, want %v", got, want)
	}
}

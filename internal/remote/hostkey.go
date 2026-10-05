package remote

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyPolicy verifies server keys against known_hosts with the MVP
// policy: known key → accept; unknown host → TOFU prompt, persist on accept;
// changed key → refuse, never bypassable.
//
// Like OpenSSH, the host key algorithms offered are narrowed to the types
// known_hosts records for the host, so a server holding several keys shows
// the one that was pinned rather than whichever x/crypto prefers.
//
// x/crypto wraps callback errors inside its handshake error, so the policy
// also records the precise error for Connect to surface.
type hostKeyPolicy struct {
	target   Target
	file     string // where TOFU writes
	db       knownHostsDB
	prompter Prompter
	// recorded are the plain key types known_hosts holds for the address
	// being dialed; empty for an unknown host.
	recorded []string
	// err is the precise verification error of the last handshake, when the
	// generic handshake error should be replaced.
	err error
}

// newHostKeyPolicy reads the known_hosts files for addr, the host:port about
// to be dialed. The first user file is created if missing, since TOFU writes
// there; the other files, and the global ones, are read when they exist.
func newHostKeyPolicy(target Target, addr string, userFiles, globalFiles []string, prompter Prompter) (*hostKeyPolicy, error) {
	file := userFiles[0]
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, fmt.Errorf("cannot prepare known_hosts: %w", err)
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare known_hosts: %w", err)
	}
	f.Close()
	files := []string{file}
	for _, other := range append(slices.Clone(userFiles[1:]), globalFiles...) {
		if readable(other) {
			files = append(files, other)
		}
	}
	db, err := loadKnownHosts(files)
	if err != nil {
		return nil, err
	}
	p := &hostKeyPolicy{target: target, file: file, db: db, prompter: prompter}
	for _, known := range db.lookup(addr) {
		if !db.isAuthority(known) && !slices.Contains(p.recorded, known.Key.Type()) {
			p.recorded = append(p.recorded, known.Key.Type())
		}
	}
	return p, nil
}

// hostKeyAlgorithms is the ClientConfig.HostKeyAlgorithms for addr: nil (the
// library default) when nothing is recorded, otherwise the algorithms able
// to show a recorded key, plus certificates when a @cert-authority applies.
func (p *hostKeyPolicy) hostKeyAlgorithms(addr string) []string {
	var authority bool
	for _, known := range p.db.lookup(addr) {
		authority = authority || p.db.isAuthority(known)
	}
	if len(p.recorded) == 0 && !authority {
		return nil
	}
	var algos []string
	if authority {
		algos = append(algos, certAlgorithms...)
		if len(p.recorded) == 0 {
			// Only a CA vouches for this host: a plain key is still unknown,
			// and handled as such.
			return append(algos, plainAlgorithms...)
		}
	}
	for _, algo := range plainAlgorithms {
		if slices.Contains(p.recorded, keyTypeOf(algo)) {
			algos = append(algos, algo)
		}
	}
	return algos
}

// plainAlgorithms and certAlgorithms are the host key algorithms offered, in
// preference order. ssh-rsa (SHA-1) is offered only for a recorded RSA key,
// after its SHA-2 signatures.
var (
	plainAlgorithms = []string{
		ssh.KeyAlgoED25519,
		ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
		ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA,
	}
	certAlgorithms = []string{
		ssh.CertAlgoED25519v01,
		ssh.CertAlgoECDSA256v01, ssh.CertAlgoECDSA384v01, ssh.CertAlgoECDSA521v01,
		ssh.CertAlgoRSASHA512v01, ssh.CertAlgoRSASHA256v01, ssh.CertAlgoRSAv01,
	}
)

// keyTypeOf maps a signature algorithm to the key type that makes it.
func keyTypeOf(algo string) string {
	switch algo {
	case ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512:
		return ssh.KeyAlgoRSA
	}
	return algo
}

func (p *hostKeyPolicy) callback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	err := p.db.check(hostname, remote, key)
	if err == nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return err // revoked key, an unvouched certificate: refuse as-is
	}
	var others bool
	for _, known := range keyErr.Want {
		if p.db.isAuthority(known) {
			continue
		}
		if known.Key.Type() == key.Type() {
			p.err = &HostKeyChangedError{
				Host: p.target.DisplayHost,
				Port: p.target.Port,
				Line: known.Line,
			}
			return p.err
		}
		others = true
	}
	if others {
		// Keys of other types are pinned and this one is not among them. The
		// algorithm restriction should have prevented it; never learn it.
		p.err = p.typeNotRecorded(key.Type())
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

func (p *hostKeyPolicy) typeNotRecorded(offered string) error {
	return &HostKeyTypeNotRecordedError{
		Host:     p.target.DisplayHost,
		Port:     p.target.Port,
		Offered:  offered,
		Recorded: p.recorded,
	}
}

// negotiationError turns x/crypto's failure to agree on a host key algorithm
// into the refusal it is when known_hosts restricted the offer.
func (p *hostKeyPolicy) negotiationError(err error) error {
	var negotiation *ssh.AlgorithmNegotiationError
	if len(p.recorded) > 0 && errors.As(err, &negotiation) && negotiation.What == "host key" {
		return p.typeNotRecorded("")
	}
	return nil
}

// learn appends the accepted key to the known_hosts file, hashing the host
// name when the file already holds hashed entries: a file kept hashed
// (OpenSSH's HashKnownHosts) does not gain a host in clear.
func (p *hostKeyPolicy) learn(hostname string, key ssh.PublicKey) error {
	hashed, err := holdsHashedHosts(p.file)
	if err != nil {
		return fmt.Errorf("cannot persist host key: %w", err)
	}
	f, err := os.OpenFile(p.file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot persist host key: %w", err)
	}
	defer f.Close()
	host := knownhosts.Normalize(hostname)
	if hashed {
		host = knownhosts.HashHostname(host)
	}
	line := knownhosts.Line([]string{host}, key)
	if _, err := fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("cannot persist host key: %w", err)
	}
	return nil
}

// holdsHashedHosts reports whether a known_hosts file has a hashed entry.
func holdsHashedHosts(file string) (bool, error) {
	f, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if bytes.HasPrefix(bytes.TrimLeft(scanner.Bytes(), " \t"), []byte("|1|")) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// readable reports whether path is a regular file this user can open.
func readable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	return err == nil && info.Mode().IsRegular()
}

// knownHostsDB is the parsed known_hosts files. knownhosts keeps its lines
// private, so @cert-authority lines are told apart by their position.
type knownHostsDB struct {
	check     ssh.HostKeyCallback
	authority map[lineRef]bool
}

type lineRef struct {
	file string
	line int
}

func loadKnownHosts(files []string) (knownHostsDB, error) {
	check, err := knownhosts.New(files...)
	if err != nil {
		return knownHostsDB{}, err
	}
	db := knownHostsDB{check: check, authority: map[lineRef]bool{}}
	for _, file := range files {
		if err := db.markAuthorities(file); err != nil {
			return knownHostsDB{}, err
		}
	}
	return db, nil
}

// markAuthorities records the @cert-authority lines of file, numbered the
// way knownhosts numbers them.
func (db *knownHostsDB) markAuthorities(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		if bytes.HasPrefix(bytes.TrimLeft(scanner.Bytes(), " \t"), []byte("@cert-authority")) {
			db.authority[lineRef{file, n}] = true
		}
	}
	return scanner.Err()
}

func (db knownHostsDB) isAuthority(known knownhosts.KnownKey) bool {
	return db.authority[lineRef{known.Filename, known.Line}]
}

// lookup returns every line that applies to addr, whatever its key. It asks
// knownhosts about a key no line can hold: the refusal lists them all.
func (db knownHostsDB) lookup(addr string) []knownhosts.KnownKey {
	err := db.check(addr, &net.TCPAddr{IP: net.IPv4zero}, probeKey{})
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		return keyErr.Want
	}
	return nil
}

// probeKey is a public key that matches no known_hosts line.
type probeKey struct{}

func (probeKey) Type() string                        { return "linqode-probe" }
func (probeKey) Marshal() []byte                     { return []byte("linqode known_hosts probe") }
func (probeKey) Verify([]byte, *ssh.Signature) error { return errors.New("probe key verifies nothing") }

// defaultKnownHostsFile is ~/.ssh/known_hosts.
func defaultKnownHostsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate known_hosts: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

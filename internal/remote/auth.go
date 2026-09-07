package remote

import (
	"errors"
	"net"
	"os"
	"slices"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// passphraseAttempts is how often a wrong passphrase may be retried before
// the connect fails, mirroring OpenSSH.
const passphraseAttempts = 3

// authCallback builds the MVP authentication ladder: SSH agent identities
// first (unless identitiesOnly), then the target's identity files, prompting
// for a passphrase only when a key is encrypted. The returned cleanup closes
// the agent connection once the handshake is over.
//
// Identity files load lazily inside their auth method, so no passphrase is
// asked for if the agent already got us in. A fatal load error (wrong
// passphrase after retries) is recorded in errOut for Connect to surface.
func authCallback(target Target, identitiesOnly bool, prompter Prompter, errOut *error) (ssh.ClientAuthCallback, func()) {
	var methods []ssh.AuthMethod
	cleanup := func() {}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" && !identitiesOnly {
		if conn, err := net.Dial("unix", sock); err == nil {
			cleanup = func() { conn.Close() }
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	for _, path := range target.IdentityFiles {
		methods = append(methods, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			signer, err := loadIdentity(path, prompter)
			if err != nil {
				*errOut = err
				return nil, err
			}
			if signer == nil {
				return nil, nil // skipped: unreadable, unsupported, or no passphrase given
			}
			return []ssh.Signer{signer}, nil
		}))
	}
	// ClientConfig.Auth only tries the first method named "publickey".
	// Select each source explicitly so a rejected or empty source advances
	// to the next one without loading later encrypted keys prematurely.
	next := func(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
		if *errOut != nil {
			return nil, *errOut
		}
		if len(methods) == 0 || !slices.Contains(ctx.AllowedMethods, "publickey") {
			return nil, nil
		}
		method := methods[0]
		methods = methods[1:]
		return method, nil
	}
	return next, cleanup
}

// loadIdentity loads one identity file, prompting for its passphrase when
// encrypted. A nil signer with nil error means "skip this key".
func loadIdentity(path string, prompter Prompter) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, nil
	}
	for range passphraseAttempts {
		passphrase, err := prompter.AskPassphrase(path)
		if err != nil {
			return nil, err
		}
		if passphrase == "" {
			return nil, nil // user skipped this key
		}
		signer, err := ssh.ParsePrivateKeyWithPassphrase(raw, []byte(passphrase))
		if err == nil {
			return signer, nil
		}
	}
	return nil, &BadPassphraseError{Path: path}
}

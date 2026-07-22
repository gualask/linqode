package remote

import "fmt"

// HostKeyRejectedError reports that the user declined an unknown host key.
type HostKeyRejectedError struct {
	Host string
	Port uint16
}

func (e *HostKeyRejectedError) Error() string {
	return fmt.Sprintf("host key for %s:%d rejected", e.Host, e.Port)
}

// HostKeyChangedError reports that the server presented a key different from
// the one recorded in known_hosts. Connecting is refused, never bypassable.
type HostKeyChangedError struct {
	Host string
	Port uint16
	// Line is the 1-indexed known_hosts line holding the conflicting key.
	Line int
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf(
		"host key for %s:%d changed (conflicts with known_hosts line %d); "+
			"refusing to connect — this may be a man-in-the-middle attack",
		e.Host, e.Port, e.Line)
}

// AuthFailedError reports that every authentication attempt was refused.
type AuthFailedError struct {
	User string
	Host string
}

func (e *AuthFailedError) Error() string {
	return fmt.Sprintf("authentication as %s@%s failed: no agent or identity key was accepted",
		e.User, e.Host)
}

// BadPassphraseError reports that decrypting an identity file kept failing
// after the allowed attempts.
type BadPassphraseError struct {
	Path string
}

func (e *BadPassphraseError) Error() string {
	return fmt.Sprintf("wrong passphrase for %s", e.Path)
}

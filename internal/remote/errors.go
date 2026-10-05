package remote

import (
	"fmt"
	"strings"
)

// UnknownHostKeyError reports an untrusted key to a non-interactive caller.
// It contains enough information for a human to verify and establish trust
// separately without allowing the machine invocation to learn the key.
type UnknownHostKeyError struct {
	Host        string
	Port        uint16
	Algorithm   string
	Fingerprint string
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("host key for %s:%d is unknown (%s %s)",
		e.Host, e.Port, e.Algorithm, e.Fingerprint)
}

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

// HostKeyTypeNotRecordedError reports a server that could not show a key of
// any type known_hosts records for it, while it does record keys of other
// types. A key of a new type is not learned on the spot — it is the shape a
// man-in-the-middle without the pinned key would take — so connecting is
// refused until the new key is verified and recorded separately.
type HostKeyTypeNotRecordedError struct {
	Host string
	Port uint16
	// Offered is the type the server showed; empty when it offered none of
	// the recorded types at all.
	Offered  string
	Recorded []string
}

func (e *HostKeyTypeNotRecordedError) Error() string {
	offered := "none of them"
	if e.Offered != "" {
		offered = "a " + e.Offered + " key"
	}
	return fmt.Sprintf(
		"known_hosts records %s keys for %s:%d and the server offered %s; "+
			"refusing to connect — verify the server's new key and add it to known_hosts",
		strings.Join(e.Recorded, ", "), e.Host, e.Port, offered)
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

// PassphraseRequiredError reports that a configured identity is encrypted
// and cannot be used by a non-interactive caller.
type PassphraseRequiredError struct {
	Path string
}

func (e *PassphraseRequiredError) Error() string {
	return fmt.Sprintf("identity %s requires a passphrase", e.Path)
}

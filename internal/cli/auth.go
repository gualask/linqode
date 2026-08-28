package cli

import "github.com/gualask/linqode/internal/remote"

// NonInteractivePrompter makes machine authentication fail closed. It never
// learns an unknown host key and never attempts to read a passphrase.
type NonInteractivePrompter struct{}

func (NonInteractivePrompter) ConfirmHostKey(
	host string,
	port uint16,
	algorithm string,
	fingerprint string,
) (bool, error) {
	return false, &remote.UnknownHostKeyError{
		Host:        host,
		Port:        port,
		Algorithm:   algorithm,
		Fingerprint: fingerprint,
	}
}

func (NonInteractivePrompter) AskPassphrase(path string) (string, error) {
	return "", &remote.PassphraseRequiredError{Path: path}
}

var _ remote.Prompter = NonInteractivePrompter{}

package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Prompter answers the interactive decisions needed while connecting.
// Implemented by the frontend: terminal prompts for the CLI, dialogs once
// the TUI owns the connect phase.
type Prompter interface {
	// ConfirmHostKey shows an unknown host's key and asks whether to trust
	// it (TOFU).
	ConfirmHostKey(host string, port uint16, algorithm, fingerprint string) (bool, error)
	// AskPassphrase asks for the passphrase of an encrypted identity file.
	// Empty means skip this key.
	AskPassphrase(path string) (string, error)
}

// ConnectOptions are knobs for how a session is established. The zero value
// matches the MVP policy (OpenSSH parity); overrides exist for special
// setups and let the integration tests stay hermetic (no touching ~/.ssh or
// the user's agent).
type ConnectOptions struct {
	// KnownHostsFile is an alternative known_hosts file, like OpenSSH's
	// UserKnownHostsFile, taking the place of the target's. Empty uses the
	// target's KnownHostsFiles, or ~/.ssh/known_hosts.
	KnownHostsFile string
	// IdentitiesOnly limits the SSH agent to the keys of the target's
	// identity files, like OpenSSH's IdentitiesOnly; so does the target's
	// own IdentitiesOnly.
	IdentitiesOnly bool
}

// ExecOutput is the collected output of a one-shot remote command.
type ExecOutput struct {
	Stdout []byte
	Stderr []byte
	// ExitCode is -1 when the remote side reported none.
	ExitCode int
}

// Session is an established SSH session. Every command runs on its own
// channel over it.
type Session struct {
	client *ssh.Client
	target Target
	// closed is closed once the connection is gone.
	closed chan struct{}
}

// Connect connects and authenticates following the MVP policy; see
// ConnectWith for the knobs.
func Connect(ctx context.Context, target Target, prompter Prompter) (*Session, error) {
	return ConnectWith(ctx, target, prompter, ConnectOptions{})
}

// ConnectWith establishes a session: host key verification per known_hosts +
// TOFU, then SSH agent identities, then the target's identity files.
func ConnectWith(ctx context.Context, target Target, prompter Prompter, opts ConnectOptions) (*Session, error) {
	setup, err := prepareConnection(target, prompter, opts)
	if err != nil {
		return nil, err
	}
	defer setup.cleanup()

	client, err := dialSSHClient(ctx, target, setup)
	if err != nil {
		return nil, err
	}
	s := &Session{client: client, target: target}
	s.watch()
	return s, nil
}

type connectionSetup struct {
	config   *ssh.ClientConfig
	policy   *hostKeyPolicy
	auth     *authState
	deadline *handshakeDeadline
	cleanup  func()
}

func prepareConnection(target Target, prompter Prompter, opts ConnectOptions) (connectionSetup, error) {
	knownHosts := target.KnownHostsFiles
	if opts.KnownHostsFile != "" {
		knownHosts = []string{opts.KnownHostsFile}
	}
	if len(knownHosts) == 0 {
		file, err := defaultKnownHostsFile()
		if err != nil {
			return connectionSetup{}, err
		}
		knownHosts = []string{file}
	}
	deadline := &handshakeDeadline{limit: target.connectTimeout()}
	prompter = pausingPrompter{Prompter: prompter, deadline: deadline}
	addr := net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
	policy, err := newHostKeyPolicy(target, addr, knownHosts, target.GlobalKnownHostsFiles, prompter)
	if err != nil {
		return connectionSetup{}, err
	}

	state := new(authState)
	auth, cleanup := authCallback(target, opts.IdentitiesOnly || target.IdentitiesOnly, prompter, state)
	return connectionSetup{
		config: &ssh.ClientConfig{
			User:            target.User,
			AuthCallback:    auth,
			HostKeyCallback: policy.callback,
			// Must match the hostname x/crypto hands the callback, which is
			// the address dialSSHClient passes to the handshake.
			HostKeyAlgorithms: policy.hostKeyAlgorithms(addr),
		},
		policy: policy, auth: state, deadline: deadline, cleanup: cleanup,
	}, nil
}

func dialSSHClient(ctx context.Context, target Target, setup connectionSetup) (*ssh.Client, error) {
	addr := net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
	dialer := net.Dialer{Timeout: setup.deadline.limit}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", addr, err)
	}

	setup.deadline.arm(conn)
	result, err := negotiateSSH(ctx, conn, addr, setup.config)
	setup.deadline.disarm()
	if ctx.Err() == nil && setup.deadline.hasExpired() {
		// Even a handshake that finished as the deadline closed the
		// connection has nothing left to run on.
		if err == nil {
			_ = result.conn.Close()
		}
		return nil, fmt.Errorf("no SSH handshake with %s within %s: %w",
			addr, setup.deadline.limit, os.ErrDeadlineExceeded)
	}
	if err != nil {
		return nil, classifyHandshakeError(err, target, setup)
	}
	return ssh.NewClient(result.conn, result.channels, result.requests), nil
}

type handshakeResult struct {
	conn     ssh.Conn
	channels <-chan ssh.NewChannel
	requests <-chan *ssh.Request
}

func negotiateSSH(ctx context.Context, conn net.Conn, addr string, config *ssh.ClientConfig) (handshakeResult, error) {
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	close(handshakeDone)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if sshConn != nil {
			_ = sshConn.Close()
		} else {
			_ = conn.Close()
		}
		return handshakeResult{}, ctxErr
	}
	if err != nil {
		_ = conn.Close()
		return handshakeResult{}, err
	}
	return handshakeResult{conn: sshConn, channels: chans, requests: reqs}, nil
}

func classifyHandshakeError(err error, target Target, setup connectionSetup) error {
	// Surface the precise cause recorded during the handshake instead of
	// x/crypto's generic wrapper.
	switch {
	case setup.policy.err != nil:
		return setup.policy.err
	case setup.policy.negotiationError(err) != nil:
		return setup.policy.negotiationError(err)
	case setup.auth.fatal != nil:
		return setup.auth.fatal
	case strings.Contains(err.Error(), "unable to authenticate"):
		if setup.auth.locked != nil {
			return setup.auth.locked
		}
		return &AuthFailedError{User: target.User, Host: target.DisplayHost}
	default:
		return err
	}
}

// Close closes the session. Errors on an already-dead connection are not
// interesting to callers.
func (s *Session) Close() {
	s.client.Close()
}

// Exec runs command and collects its output until it finishes or ctx is
// cancelled.
func (s *Session) Exec(ctx context.Context, command string) (ExecOutput, error) {
	sess, err := s.newSession(ctx)
	if err != nil {
		return ExecOutput{}, err
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout, sess.Stderr = &stdout, &stderr

	err = s.runSession(ctx, sess, func() error { return sess.Run(command) })
	if ctx.Err() != nil {
		return ExecOutput{}, ctx.Err()
	}

	out := ExecOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: -1}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case err == nil:
		out.ExitCode = 0
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitStatus()
	case errors.As(err, &missing):
		// Ran to completion but the server reported no status; keep -1.
	default:
		return out, err
	}
	return out, nil
}

// terminate ends a remote command: terminate signal, then channel close.
func terminate(sess *ssh.Session) {
	_ = sess.Signal(ssh.SIGTERM)
	sess.Close()
}

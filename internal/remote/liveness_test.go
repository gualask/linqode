package remote_test

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/remote"
)

// A server that accepts the TCP connection and never speaks SSH must not
// hold Connect until the caller gives up: the handshake has a deadline of
// its own.
func TestConnectTimesOutOnSilentServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	target := remote.Target{
		Host: "127.0.0.1", DisplayHost: "127.0.0.1", User: testUser,
		Port:           uint16(listener.Addr().(*net.TCPAddr).Port),
		ConnectTimeout: 200 * time.Millisecond,
	}
	started := time.Now()
	_, err = remote.ConnectWith(testContext(t), target, &prompter{}, remote.ConnectOptions{
		KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), IdentitiesOnly: true,
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the handshake's own timeout", err)
	}
	if elapsed := time.Since(started); elapsed > guardTimeout/2 {
		t.Fatalf("Connect took %s against a silent server", elapsed)
	}
}

// slowPrompter takes longer to answer than the connect timeout, as a person
// reading a fingerprint does.
type slowPrompter struct {
	prompter
	delay time.Duration
}

func (p *slowPrompter) ConfirmHostKey(host string, port uint16, algorithm, fingerprint string) (bool, error) {
	time.Sleep(p.delay)
	return p.prompter.ConfirmHostKey(host, port, algorithm, fingerprint)
}

func TestConnectTimeoutExcludesPrompts(t *testing.T) {
	server := spawn(t, nil)
	target := server.target()
	target.ConnectTimeout = 100 * time.Millisecond
	p := &slowPrompter{prompter: prompter{acceptHostKey: true}, delay: 400 * time.Millisecond}
	session, err := remote.ConnectWith(testContext(t), target, p, server.options())
	if err != nil {
		t.Fatalf("time spent at the prompt counted against the handshake: %v", err)
	}
	session.Close()
}

// freezingProxy forwards TCP to the fixture until frozen, then swallows
// everything in both directions: a peer that vanished without a FIN.
type freezingProxy struct {
	port   uint16
	frozen atomic.Bool
}

func newFreezingProxy(t *testing.T, upstream uint16) *freezingProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	p := &freezingProxy{port: uint16(listener.Addr().(*net.TCPAddr).Port)}
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(upstream))))
			if err != nil {
				client.Close()
				return
			}
			t.Cleanup(func() { client.Close(); server.Close() })
			go p.pipe(server, client)
			go p.pipe(client, server)
		}
	}()
	return p
}

func (p *freezingProxy) pipe(dst io.Writer, src io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.frozen.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// A connection whose peer went silent must be given up by the keepalive,
// so a command waiting on it fails instead of hanging for good.
func TestKeepaliveClosesSilentConnection(t *testing.T) {
	server := spawn(t, map[string]script{"true": {}})
	proxy := newFreezingProxy(t, server.port)
	target := server.target()
	target.Port = proxy.port
	target.ServerAliveInterval = 50 * time.Millisecond
	target.ServerAliveCountMax = 2
	session, err := remote.ConnectWith(testContext(t), target, &prompter{acceptHostKey: true}, server.options())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// Several intervals of a live peer keep the connection.
	time.Sleep(300 * time.Millisecond)
	if _, err := session.Exec(testContext(t), "true"); err != nil {
		t.Fatalf("keepalive dropped a live connection: %v", err)
	}

	proxy.frozen.Store(true)
	started := time.Now()
	_, err = session.Exec(testContext(t), "true")
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the connection closed by the keepalive", err)
	}
	if elapsed := time.Since(started); elapsed > guardTimeout/2 {
		t.Fatalf("command waited %s on a silent peer", elapsed)
	}
}

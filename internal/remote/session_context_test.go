package remote_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gualask/linqode/internal/remote"
)

func TestCommandCancellationDuringSSHWaits(t *testing.T) {
	for _, mode := range []string{"exec", "stream"} {
		for _, stage := range []string{"channel open", "exec reply", "after output EOF"} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				reached := make(chan struct{})
				session := protocolSession(t, func(next ssh.NewChannel, stop <-chan struct{}) {
					if stage == "channel open" {
						close(reached)
						<-stop
						return
					}
					channel, requests, err := next.Accept()
					if err != nil {
						return
					}
					defer channel.Close()
					for request := range requests {
						if request.Type != "exec" {
							continue
						}
						var payload struct{ Command string }
						_ = ssh.Unmarshal(request.Payload, &payload)
						if payload.Command == "probe" {
							_ = request.Reply(true, nil)
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
						if stage == "after output EOF" {
							_ = request.Reply(true, nil)
							_ = channel.CloseWrite()
						}
						close(reached)
					}
				})
				ctx, cancel := context.WithCancel(testContext(t))
				defer cancel()
				done := make(chan error, 1)
				started := make(chan struct{})
				go func() {
					if mode == "exec" {
						_, err := session.Exec(ctx, "stall")
						done <- err
						return
					}
					events, err := session.ExecStream(ctx, "stall")
					close(started)
					if err == nil {
						for range events {
						}
					}
					done <- err
				}()
				select {
				case <-reached:
				case <-time.After(guardTimeout):
					t.Fatal("command did not reach the stalled phase")
				}
				if mode == "stream" && stage == "after output EOF" {
					select {
					case <-started:
					case <-time.After(guardTimeout):
						t.Fatal("acknowledged stream did not start")
					}
				}
				cancel()
				select {
				case err := <-done:
					if !(mode == "stream" && stage == "after output EOF") && !errors.Is(err, context.Canceled) {
						t.Fatalf("error = %v, want context.Canceled", err)
					}
				case <-time.After(guardTimeout / 2):
					t.Fatal("command remained blocked after cancellation")
				}
				if stage != "channel open" {
					if out, err := session.Exec(testContext(t), "probe"); err != nil || out.ExitCode != 0 {
						t.Fatalf("cancelling one command broke the shared connection: %+v, %v", out, err)
					}
				}
			})
		}
	}
}

func TestAlreadyCancelledCommandDoesNotStart(t *testing.T) {
	server := spawn(t, map[string]script{"probe": {}})
	session := connect(t, server, &prompter{acceptHostKey: true})
	ctx, cancel := context.WithCancel(testContext(t))
	cancel()
	if _, err := session.Exec(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec error = %v", err)
	}
	if _, err := session.ExecStream(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecStream error = %v", err)
	}
	if out, err := session.Exec(testContext(t), "probe"); err != nil || out.ExitCode != 0 {
		t.Fatalf("already cancelled commands broke the connection: %+v, %v", out, err)
	}
}

// protocolSession lets tests withhold SSH replies that the scripted command
// fixture normally sends automatically.
func protocolSession(t *testing.T, handle func(ssh.NewChannel, <-chan struct{})) *remote.Session {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(genSigner(t))
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(guardTimeout))
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			handle(channel, stop)
		}
	}()
	session, err := remote.ConnectWith(testContext(t), remote.Target{
		Host: "127.0.0.1", DisplayHost: "127.0.0.1", User: testUser,
		Port: uint16(listener.Addr().(*net.TCPAddr).Port),
	}, &prompter{acceptHostKey: true}, remote.ConnectOptions{
		KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), IdentitiesOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

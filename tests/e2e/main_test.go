//go:build e2e

// Package e2e drives the tests/fixture container — sshd in front of a real
// docker-in-docker daemon — with the production packages, covering the one
// path the offline suite cannot: that the commands Linqode builds actually
// produce the output its parsers expect, on a live Docker.
//
// It is behind the `e2e` build tag, so `go test ./...` stays offline:
//
//	docker compose -f tests/fixture/docker-compose.yml build   # optional, warms the cache
//	go test -tags e2e -timeout 20m ./tests/e2e/
//
// Add -fixture.keep to leave the container running for inspection:
//
//	ssh -p 2222 -i tests/fixture/.keys/id_ed25519 linqode@127.0.0.1
package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/remote"
)

const (
	fixtureUser = "linqode"
	fixtureHost = "127.0.0.1"
	fixturePort = 2222
	// composeDir is where the Dockerfile puts the demo project.
	composeDir = "/srv/demo"
	// demoServices is what the demo project defines; readiness means all of
	// them are reported by `compose ps`.
	demoServices = 5
	// demoRunningServices excludes `migrate`, which exits immediately, so
	// it never appears in a live resource stream.
	demoRunningServices = 4
)

// guardTimeout bounds a single operation inside a test, mirroring the
// convention of the in-process SSH suite: a regression hangs the test rather
// than CI.
const guardTimeout = 30 * time.Second

var keepFixture = flag.Bool("fixture.keep", false, "leave the fixture container running after the tests")

// fixtureDir is tests/fixture, relative to this package.
var fixtureDir = filepath.Join("..", "fixture")

// identityFile is the private key the fixture authorizes, generated per run.
var identityFile string

// runKnownHosts is a known_hosts file scoped to this run. It must not
// outlive it: the fixture generates a fresh host key per container, so a
// reused file would look like a changed key and be refused — correctly.
var runKnownHosts string

func TestMain(m *testing.M) {
	flag.Parse()

	if err := startFixture(); err != nil {
		fmt.Fprintf(os.Stderr, "fixture setup failed: %v\n", err)
		stopFixture()
		os.Exit(1)
	}

	code := m.Run()

	if *keepFixture {
		fmt.Fprintf(os.Stderr, "fixture left running (-fixture.keep); stop it with:\n"+
			"  docker compose -f %s/docker-compose.yml down -v\n", fixtureDir)
	} else {
		stopFixture()
	}
	if runKnownHosts != "" {
		_ = os.RemoveAll(filepath.Dir(runKnownHosts))
	}
	os.Exit(code)
}

// startFixture generates the authorized key, brings the container up, and
// waits until sshd answers and the demo project is running.
func startFixture() error {
	keyDir := filepath.Join(fixtureDir, ".keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return err
	}
	var err error
	if identityFile, err = ensureKeyPair(keyDir); err != nil {
		return fmt.Errorf("generating fixture key: %w", err)
	}
	if err := ensureHostKey(keyDir); err != nil {
		return fmt.Errorf("generating fixture host key: %w", err)
	}

	runDir, err := os.MkdirTemp("", "linqode-e2e-")
	if err != nil {
		return err
	}
	runKnownHosts = filepath.Join(runDir, "known_hosts")

	// The first run builds the image and the inner daemon pulls busybox, so
	// this is slow; later runs reuse both.
	up := exec.Command("docker", "compose", "up", "-d", "--build")
	up.Dir = fixtureDir
	up.Stdout, up.Stderr = os.Stderr, os.Stderr
	if err := up.Run(); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	return waitReady(5 * time.Minute)
}

func stopFixture() {
	down := exec.Command("docker", "compose", "down", "-v")
	down.Dir = fixtureDir
	down.Stdout, down.Stderr = os.Stderr, os.Stderr
	_ = down.Run()
}

// ensureKeyPair returns the path of the ed25519 identity the fixture
// authorizes, generating it and its authorized_keys entry on first use.
//
// Keys are generated rather than checked in, but kept once generated: a
// stable identity means `ssh-add` is needed only once when driving the TUI
// against the fixture by hand. Delete .keys/ to force new ones.
func ensureKeyPair(dir string) (string, error) {
	keyPath, authorized := filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "authorized_keys")
	if exists(keyPath) && exists(authorized) {
		return keyPath, nil
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(authorized, ssh.MarshalAuthorizedKey(sshPublic), 0o644); err != nil {
		return "", err
	}
	return keyPath, nil
}

// ensureHostKey generates the server's own key on first use, so the fixture
// keeps its identity across recreations.
//
// Without this, every new container would present a new host key, and a
// client that had already trusted the old one would refuse to connect —
// correctly, but making the fixture unusable for repeated manual runs. The
// e2e tests are unaffected either way: they use a per-run known_hosts, so
// TOFU is exercised from an empty file regardless.
func ensureHostKey(dir string) error {
	hostKey := filepath.Join(dir, "ssh_host_ed25519_key")
	if exists(hostKey) {
		return nil
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return err
	}
	return os.WriteFile(hostKey, pem.EncodeToMemory(block), 0o600)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// waitReady polls until the fixture answers SSH and `compose ps` reports the
// whole demo project. Both have to hold: sshd comes up before dockerd, which
// comes up before the project.
func waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		session, err := remote.ConnectWith(ctx, fixtureTarget(), acceptingPrompter{},
			remote.ConnectOptions{KnownHostsFile: runKnownHosts, IdentitiesOnly: true})
		if err != nil {
			cancel()
			// A changed host key or a rejected identity never resolves by
			// waiting; failing now surfaces the cause instead of burying it
			// under a timeout.
			var changed *remote.HostKeyChangedError
			var auth *remote.AuthFailedError
			if errors.As(err, &changed) || errors.As(err, &auth) {
				return fmt.Errorf("connect: %w", err)
			}
			last = fmt.Errorf("connect: %w", err)
			continue
		}
		out, err := session.Exec(ctx, compose.PsCommand(composeDir))
		session.Close()
		cancel()
		if err != nil {
			last = fmt.Errorf("compose ps: %w", err)
			continue
		}
		if out.ExitCode != 0 {
			last = fmt.Errorf("compose ps exited %d: %s", out.ExitCode, out.Stderr)
			continue
		}
		services, err := compose.ParsePS(out.Stdout)
		if err != nil {
			last = fmt.Errorf("parsing ps: %w", err)
			continue
		}
		if len(services) < demoServices {
			last = fmt.Errorf("only %d/%d services up", len(services), demoServices)
			continue
		}
		return nil
	}
	return fmt.Errorf("fixture not ready within %s: %w", timeout, last)
}

// fixtureTarget is the resolved target for the fixture, bypassing
// ~/.ssh/config resolution: the fixture is addressed directly.
func fixtureTarget() remote.Target {
	return remote.Target{
		Host:          fixtureHost,
		DisplayHost:   fixtureHost,
		Port:          fixturePort,
		User:          fixtureUser,
		IdentityFiles: []string{identityFile},
	}
}

// acceptingPrompter trusts the fixture's host key on first use and never
// needs a passphrase; tests that care about the prompting itself count calls
// with countingPrompter instead.
type acceptingPrompter struct{}

func (acceptingPrompter) ConfirmHostKey(string, uint16, string, string) (bool, error) {
	return true, nil
}
func (acceptingPrompter) AskPassphrase(string) (string, error) { return "", nil }

// connect opens a session against the fixture with a per-test known_hosts,
// so each test exercises the trust-on-first-use path in isolation.
func connect(t *testing.T) *remote.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()

	session, err := remote.ConnectWith(ctx, fixtureTarget(), acceptingPrompter{}, remote.ConnectOptions{
		KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"),
		IdentitiesOnly: true,
	})
	if err != nil {
		t.Fatalf("connecting to fixture: %v", err)
	}
	t.Cleanup(session.Close)
	return session
}

package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kevinburke/ssh_config"
)

func TestParsesBareHost(t *testing.T) {
	s, err := parseSpec("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := (hostSpec{host: "example.com"}); s != want {
		t.Errorf("got %+v, want %+v", s, want)
	}
}

func TestParsesFullSpec(t *testing.T) {
	s, err := parseSpec("deploy@203.0.113.10:2222")
	if err != nil {
		t.Fatal(err)
	}
	if want := (hostSpec{user: "deploy", host: "203.0.113.10", port: 2222}); s != want {
		t.Errorf("got %+v, want %+v", s, want)
	}
}

func TestUserPartEndsAtLastAtSign(t *testing.T) {
	s, err := parseSpec("we@ird@host")
	if err != nil {
		t.Fatal(err)
	}
	if s.user != "we@ird" || s.host != "host" {
		t.Errorf("got %+v", s)
	}
}

func TestBareIPv6IsNotAPort(t *testing.T) {
	s, err := parseSpec("root@fe80::1")
	if err != nil {
		t.Fatal(err)
	}
	if s.host != "fe80::1" || s.port != 0 {
		t.Errorf("got %+v", s)
	}
}

func TestRejectsBadSpecs(t *testing.T) {
	for _, spec := range []string{"", "@host", "user@", "host:", "host:notaport", "host:0", "user@:22"} {
		if _, err := parseSpec(spec); err == nil {
			t.Errorf("should reject %q", spec)
		}
	}
}

func decode(t *testing.T, raw string) lookup {
	t.Helper()
	cfg, err := ssh_config.Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return configLookup(cfg)
}

const sampleSSHConfig = "Host myapp-prod\n  HostName 203.0.113.10\n  User deploy\n  Port 2200\n"

func TestResolvesAliasFromSSHConfig(t *testing.T) {
	tgt, err := resolveWith("myapp-prod", decode(t, sampleSSHConfig), "")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Host != "203.0.113.10" || tgt.DisplayHost != "myapp-prod" ||
		tgt.Port != 2200 || tgt.User != "deploy" {
		t.Errorf("got %+v", tgt)
	}
}

func TestSpecOverridesSSHConfig(t *testing.T) {
	tgt, err := resolveWith("root@myapp-prod:22", decode(t, sampleSSHConfig), "")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Host != "203.0.113.10" || tgt.Port != 22 || tgt.User != "root" {
		t.Errorf("got %+v", tgt)
	}
}

func TestDefaultIdentitiesOnlyExistingFiles(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(sshDir, "id_ed25519")
	if err := os.WriteFile(key, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}

	tgt, err := resolveWith("user@example.com", decode(t, ""), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgt.IdentityFiles) != 1 || tgt.IdentityFiles[0] != key {
		t.Errorf("got %v, want [%s]", tgt.IdentityFiles, key)
	}
}

func TestConfiguredIdentityFileExpandsTilde(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(sshDir, "deploy_key")
	if err := os.WriteFile(key, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := decode(t, "Host myapp\n  IdentityFile ~/.ssh/deploy_key\n")
	tgt, err := resolveWith("user@myapp", cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgt.IdentityFiles) != 1 || tgt.IdentityFiles[0] != key {
		t.Errorf("got %v, want [%s]", tgt.IdentityFiles, key)
	}
}

func TestTimeoutsAndKeepalivesFromSSHConfig(t *testing.T) {
	cfg := decode(t, "Host slow\n  ConnectTimeout 40\n  ServerAliveInterval 5\n  ServerAliveCountMax 6\n"+
		"Host quiet\n  ServerAliveInterval 0\n")
	tgt, err := resolveWith("user@slow", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.ConnectTimeout != 40*time.Second || tgt.ServerAliveInterval != 5*time.Second || tgt.ServerAliveCountMax != 6 {
		t.Errorf("got %+v", tgt)
	}
	if interval, count := tgt.serverAlive(); interval != 5*time.Second || count != 6 {
		t.Errorf("serverAlive() = %s, %d", interval, count)
	}

	tgt, err = resolveWith("user@quiet", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if interval, _ := tgt.serverAlive(); interval != 0 {
		t.Errorf("ServerAliveInterval 0 left keepalives on every %s", interval)
	}
	if tgt.connectTimeout() != defaultConnectTimeout {
		t.Errorf("connect timeout %s, want the default", tgt.connectTimeout())
	}

	tgt, err = resolveWith("user@unconfigured", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if interval, count := tgt.serverAlive(); interval != defaultServerAliveInterval || count != defaultServerAliveCountMax {
		t.Errorf("defaults: serverAlive() = %s, %d", interval, count)
	}
}

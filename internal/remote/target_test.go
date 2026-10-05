package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestBracketedIPv6(t *testing.T) {
	for spec, want := range map[string]hostSpec{
		"[::1]:2222":             {host: "::1", port: 2222},
		"user@[::1]":             {user: "user", host: "::1"},
		"[::1]":                  {host: "::1"},
		"root@[fe80::1%eth0]:22": {user: "root", host: "fe80::1%eth0", port: 22},
	} {
		s, err := parseSpec(spec)
		if err != nil {
			t.Errorf("%s: %v", spec, err)
			continue
		}
		if s != want {
			t.Errorf("%s: got %+v, want %+v", spec, s, want)
		}
	}
}

func TestRejectsBadSpecs(t *testing.T) {
	for _, spec := range []string{"", "@host", "user@", "host:", "host:notaport", "host:0", "user@:22",
		"[::1", "[]", "[::1]x", "[::1]:", "[::1]:0", "[host]"} {
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

func TestSSHConfigIdentitiesOnly(t *testing.T) {
	cfg := decode(t, "Host pinned\n  IdentitiesOnly yes\n")
	tgt, err := resolveWith("user@pinned", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !tgt.IdentitiesOnly {
		t.Errorf("got %+v", tgt)
	}
	tgt, err = resolveWith("user@plain", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.IdentitiesOnly {
		t.Errorf("unconfigured host got %+v", tgt)
	}
}

func TestJumpHostsAreRefusedNotBypassed(t *testing.T) {
	cfg := decode(t, "Host behind\n  ProxyJump bastion\n"+
		"Host piped\n  ProxyCommand ssh -W %h:%p bastion\n"+
		"Host direct\n  ProxyCommand none\n  ProxyJump none\n")
	for _, host := range []string{"behind", "piped"} {
		_, err := resolveWith("user@"+host, cfg, "")
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: got %v, want an unsupported-proxy error", host, err)
		}
	}
	if _, err := resolveWith("user@direct", cfg, ""); err != nil {
		t.Errorf("explicit none refused: %v", err)
	}
}

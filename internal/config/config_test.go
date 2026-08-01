package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const sample = `
[hosts.myapp]
host = "deploy@203.0.113.10"
compose_dir = "/srv/myapp"

[hosts.other]
host = "other-prod"
`

func mustParse(t *testing.T, raw string) *Config {
	t.Helper()
	cfg, err := parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestParsesDocumentedFormat(t *testing.T) {
	cfg := mustParse(t, sample)
	if cfg.Hosts["myapp"].Host != "deploy@203.0.113.10" {
		t.Errorf("got %+v", cfg.Hosts["myapp"])
	}
	if cfg.Hosts["myapp"].ComposeDir != "/srv/myapp" {
		t.Errorf("got %+v", cfg.Hosts["myapp"])
	}
	if cfg.Hosts["other"].ComposeDir != "" {
		t.Errorf("got %+v", cfg.Hosts["other"])
	}
}

func TestToleratesFutureSections(t *testing.T) {
	raw := "[hosts.a]\nhost = \"h\"\nfuture_knob = true\n[future_section]\nx = 1\n"
	if _, err := parse([]byte(raw)); err != nil {
		t.Errorf("should tolerate unknown fields: %v", err)
	}
}

func TestRejectsMissingHost(t *testing.T) {
	if _, err := parse([]byte("[hosts.a]\ncompose_dir = \"/srv\"\n")); err == nil {
		t.Error("should reject an entry without host")
	}
}

func TestParsesScriptsSortedByName(t *testing.T) {
	raw := "[hosts.a]\nhost = \"h\"\n[hosts.a.scripts]\nmem = \"free -m\"\ndisk = \"df -h\"\n"
	sel, err := mustParse(t, raw).Select("a")
	if err != nil {
		t.Fatal(err)
	}
	want := []Script{{"disk", "df -h"}, {"mem", "free -m"}}
	if !slices.Equal(sel.Scripts, want) {
		t.Errorf("got %v, want %v", sel.Scripts, want)
	}

	// Absent table and inline specs mean no scripts.
	bare := mustParse(t, "[hosts.b]\nhost = \"h\"\n")
	for _, arg := range []string{"b", "x@y"} {
		sel, err := bare.Select(arg)
		if err != nil {
			t.Fatal(err)
		}
		if len(sel.Scripts) != 0 {
			t.Errorf("Select(%q): unexpected scripts %v", arg, sel.Scripts)
		}
	}
}

func TestSelectsByName(t *testing.T) {
	sel, err := mustParse(t, sample).Select("myapp")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Spec != "deploy@203.0.113.10" || sel.ComposeDir != "/srv/myapp" {
		t.Errorf("got %+v", sel)
	}
}

func TestUnknownNameIsInlineSpec(t *testing.T) {
	sel, err := mustParse(t, sample).Select("root@example.com:2222")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Spec != "root@example.com:2222" || sel.ComposeDir != "" {
		t.Errorf("got %+v", sel)
	}
}

func TestNoArgNeedsExactlyOneHost(t *testing.T) {
	one := mustParse(t, "[hosts.a]\nhost = \"user@h\"\n")
	sel, err := one.Select("")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Spec != "user@h" {
		t.Errorf("got %+v", sel)
	}

	if _, err := (&Config{}).Select(""); err == nil {
		t.Error("empty config should refuse an absent host arg")
	}
	if _, err := mustParse(t, sample).Select(""); err == nil {
		t.Error("multiple hosts should refuse an absent host arg")
	}
}

// Host metrics are cheap enough to be on by default, but a server where
// even one extra command is unwelcome must be able to turn them off.
func TestHostMetricsDefaultOnAndDisablable(t *testing.T) {
	cfg := mustParse(t, `
[hosts.unset]
host = "user@a"

[hosts.off]
host = "user@b"
host_metrics = false

[hosts.on]
host = "user@c"
host_metrics = true
`)
	for name, want := range map[string]bool{"unset": true, "off": false, "on": true} {
		sel, err := cfg.Select(name)
		if err != nil {
			t.Fatalf("selecting %q: %v", name, err)
		}
		if sel.HostMetrics != want {
			t.Errorf("hosts.%s: HostMetrics = %v, want %v", name, sel.HostMetrics, want)
		}
	}

	// An inline spec has no config entry to read the flag from, and must
	// still get the header.
	sel, err := (&Config{}).Select("user@inline")
	if err != nil {
		t.Fatal(err)
	}
	if !sel.HostMetrics {
		t.Error("inline host spec should enable host metrics")
	}
}

func TestLoadDefaults(t *testing.T) {
	// A missing default config is an empty config…
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 0 {
		t.Errorf("got %+v", cfg.Hosts)
	}
	// …but a missing explicit path is an error.
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Error("explicit missing path should fail")
	}
	// The default path is read when the file exists.
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".config", "linqode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 2 {
		t.Errorf("got %+v", cfg.Hosts)
	}
}

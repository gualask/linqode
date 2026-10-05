// Package remote implements the SSH side of linqode: resolving connection
// targets against ~/.ssh/config and, from G1 on, connecting and executing
// commands.
package remote

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
)

// defaultIdentities are the identity files tried in order when none is
// configured, mirroring OpenSSH.
var defaultIdentities = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// Target is a fully resolved connection target.
type Target struct {
	// Host name or address to connect to (after ~/.ssh/config resolution).
	Host string
	// DisplayHost is the name used for known_hosts lookup and display: the
	// alias or host as the user typed it, before HostName substitution.
	DisplayHost string
	Port        uint16
	User        string
	// IdentityFiles to try after the agent, in order. Only existing files.
	IdentityFiles []string
	// ConnectTimeout bounds the TCP connect and the SSH handshake, time
	// spent at a prompt excluded. Zero uses defaultConnectTimeout.
	ConnectTimeout time.Duration
	// ServerAliveInterval is how often the server is asked whether it is
	// still there; after ServerAliveCountMax unanswered asks in a row the
	// connection is closed. Zero uses defaultServerAliveInterval, negative
	// disables keepalives. A zero count uses defaultServerAliveCountMax.
	ServerAliveInterval time.Duration
	ServerAliveCountMax int
}

// hostSpec is the user/host/port split of a raw spec, before ssh_config
// resolution. Zero port means unset.
type hostSpec struct {
	user string
	host string
	port uint16
}

// parseSpec parses `[user@]host[:port]`.
func parseSpec(spec string) (hostSpec, error) {
	bad := fmt.Errorf("invalid host spec %q (expected [user@]host[:port])", spec)
	var s hostSpec
	rest := spec
	// Like OpenSSH, the user part ends at the last `@`.
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		if i == 0 {
			return s, bad
		}
		s.user, rest = rest[:i], rest[i+1:]
	}
	// A second `:` before the split means a bare IPv6 address, not a port.
	if i := strings.LastIndexByte(rest, ':'); i >= 0 && !strings.Contains(rest[:i], ":") {
		p, err := strconv.ParseUint(rest[i+1:], 10, 16)
		if err != nil || p == 0 {
			return s, bad
		}
		s.port = uint16(p)
		rest = rest[:i]
	}
	if rest == "" {
		return s, bad
	}
	s.host = rest
	return s, nil
}

// lookup returns the explicit ssh_config values of key for alias, in file
// order; nil when the key is not set. Library defaults are never included.
type lookup func(alias, key string) []string

// Resolve resolves a host spec against ~/.ssh/config and local defaults.
//
// Explicit user/port in the spec win over ssh_config values, which win over
// defaults (local user name, port 22, ~/.ssh/id_* identities).
func Resolve(spec string) (Target, error) {
	home, _ := os.UserHomeDir()
	return resolveWith(spec, userConfigLookup(home), home)
}

func resolveWith(spec string, cfg lookup, home string) (Target, error) {
	parsed, err := parseSpec(spec)
	if err != nil {
		return Target{}, err
	}
	first := func(key string) string {
		if vals := cfg(parsed.host, key); len(vals) > 0 {
			return vals[0]
		}
		return ""
	}

	t := Target{Host: parsed.host, DisplayHost: parsed.host, Port: 22, User: parsed.user}
	if h := first("HostName"); h != "" {
		t.Host = h
	}
	if parsed.port != 0 {
		t.Port = parsed.port
	} else if p, err := strconv.ParseUint(first("Port"), 10, 16); err == nil && p != 0 {
		t.Port = uint16(p)
	}
	if t.User == "" {
		t.User = first("User")
	}
	if t.User == "" {
		t.User = localUser()
	}
	if t.User == "" {
		return Target{}, errors.New("no user: none in the spec, ~/.ssh/config, or environment")
	}

	if secs, ok := seconds(first("ConnectTimeout")); ok && secs > 0 {
		t.ConnectTimeout = secs
	}
	if secs, ok := seconds(first("ServerAliveInterval")); ok {
		t.ServerAliveInterval = secs
		if secs == 0 {
			t.ServerAliveInterval = -1 // explicitly off, as in OpenSSH
		}
	}
	if n, err := strconv.Atoi(first("ServerAliveCountMax")); err == nil && n > 0 {
		t.ServerAliveCountMax = n
	}

	files := cfg(parsed.host, "IdentityFile")
	if len(files) == 0 && home != "" {
		for _, name := range defaultIdentities {
			files = append(files, filepath.Join(home, ".ssh", name))
		}
	}
	for _, f := range files {
		f = expandHome(f, home)
		if fi, err := os.Stat(f); err == nil && fi.Mode().IsRegular() {
			t.IdentityFiles = append(t.IdentityFiles, f)
		}
	}
	return t, nil
}

// userConfigLookup reads ~/.ssh/config once; a missing or unparsable file
// resolves every key to unset.
func userConfigLookup(home string) lookup {
	none := func(string, string) []string { return nil }
	if home == "" {
		return none
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return none
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return none
	}
	return configLookup(cfg)
}

// configLookup adapts a decoded ssh_config file to the lookup interface.
func configLookup(cfg *ssh_config.Config) lookup {
	return func(alias, key string) []string {
		vals, err := cfg.GetAll(alias, key)
		if err != nil {
			return nil
		}
		return vals
	}
}

// seconds parses an ssh_config duration given in whole seconds.
func seconds(value string) (time.Duration, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

func expandHome(path, home string) string {
	if home != "" && strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func localUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	for _, env := range []string{"USER", "USERNAME"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return ""
}

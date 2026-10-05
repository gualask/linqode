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

// defaultGlobalKnownHostsFiles are OpenSSH's system-wide known_hosts files,
// read when they exist.
var defaultGlobalKnownHostsFiles = []string{"/etc/ssh/ssh_known_hosts", "/etc/ssh/ssh_known_hosts2"}

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
	// IdentitiesOnly limits the agent to the keys of IdentityFiles, like
	// OpenSSH's IdentitiesOnly.
	IdentitiesOnly bool
	// KnownHostsFiles are the user's known_hosts files, like OpenSSH's
	// UserKnownHostsFile: all are consulted, trust-on-first-use writes to
	// the first. Empty uses ~/.ssh/known_hosts.
	KnownHostsFiles []string
	// GlobalKnownHostsFiles are consulted too, never written.
	GlobalKnownHostsFiles []string
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

// parseSpec parses `[user@]host[:port]`, where host may be a bracketed IPv6
// address (`[::1]:2222`) or a bare one without a port (`fe80::1`).
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
	if bracketed, ok := strings.CutPrefix(rest, "["); ok {
		host, after, closed := strings.Cut(bracketed, "]")
		// Brackets are for IPv6 addresses only, and only a port may follow.
		if !closed || !strings.Contains(host, ":") {
			return s, bad
		}
		if after != "" {
			portText, ok := strings.CutPrefix(after, ":")
			if !ok || !parsePort(portText, &s.port) {
				return s, bad
			}
		}
		s.host = host
		return s, nil
	}
	// A second `:` before the split means a bare IPv6 address, not a port.
	if i := strings.LastIndexByte(rest, ':'); i >= 0 && !strings.Contains(rest[:i], ":") {
		if !parsePort(rest[i+1:], &s.port) {
			return s, bad
		}
		rest = rest[:i]
	}
	if rest == "" {
		return s, bad
	}
	s.host = rest
	return s, nil
}

// parsePort stores a valid, non-zero port in port.
func parsePort(text string, port *uint16) bool {
	p, err := strconv.ParseUint(text, 10, 16)
	if err != nil || p == 0 {
		return false
	}
	*port = uint16(p)
	return true
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

	// Jump hosts are not supported. Connecting directly instead would take
	// another network path than ssh does, or reach nothing at all.
	for _, key := range []string{"ProxyJump", "ProxyCommand"} {
		if v := first(key); v != "" && !strings.EqualFold(v, "none") {
			return Target{}, fmt.Errorf(
				"%s: ~/.ssh/config sets %s, which is not supported yet: "+
					"Linqode only connects directly", parsed.host, key)
		}
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

	t.IdentitiesOnly = strings.EqualFold(first("IdentitiesOnly"), "yes")
	t.KnownHostsFiles = pathList(cfg(parsed.host, "UserKnownHostsFile"), home)
	t.GlobalKnownHostsFiles = pathList(cfg(parsed.host, "GlobalKnownHostsFile"), home)
	if t.GlobalKnownHostsFiles == nil {
		t.GlobalKnownHostsFiles = defaultGlobalKnownHostsFiles
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

// pathList splits ssh_config values that hold several paths each, as the
// known_hosts options do, expanding a leading ~/. "none" names no file.
func pathList(values []string, home string) []string {
	var paths []string
	for _, v := range values {
		for _, path := range strings.Fields(v) {
			if !strings.EqualFold(path, "none") {
				paths = append(paths, expandHome(path, home))
			}
		}
	}
	return paths
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

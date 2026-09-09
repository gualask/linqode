// Package config loads ~/.config/linqode/config.toml (format documented in
// README.md) and picks the host the session will connect to.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// LocalSpec is the `host` value that means the machine Linqode is running
// on rather than one to reach over SSH. It is a target value, not a mode:
// `linqode local` works with no config at all, and a named entry giving it a
// project is an ordinary host entry. It is deliberately not `localhost`,
// which is a real name meaning something else — `ssh localhost` goes through
// sshd, possibly as another user — and which this would make inexpressible.
//
// A user who configures `[hosts.local]` wins over the keyword: Select looks
// the name up before falling through to an inline spec.
const LocalSpec = "local"

type Config struct {
	Hosts map[string]Host `toml:"hosts"`
}

type Host struct {
	// Host is a ~/.ssh/config alias, an inline `[user@]host[:port]`, or
	// LocalSpec.
	Host string `toml:"host"`
	// ComposeDir is the directory on the server containing compose.yaml.
	ComposeDir string `toml:"compose_dir"`
	// Scripts are predefined server commands runnable by name from either
	// presentation adapter. Only the TUI can run an ad-hoc command.
	Scripts map[string]string `toml:"scripts"`
	// HostMetrics enables the TUI's resource views and the machine stats
	// command; unset means enabled.
	// A pointer distinguishes "not configured" from an explicit false.
	HostMetrics *bool `toml:"host_metrics"`
}

// Selection is the host the session will connect to, after CLI/config
// selection.
type Selection struct {
	Spec       string
	ComposeDir string
	Scripts    []Script
	// HostMetrics is whether to sample resource usage — the machine's, and
	// the containers'. Enabled unless the config turns it off.
	HostMetrics bool
}

// Script is a predefined command from the config.
type Script struct {
	Name    string
	Command string
}

// Load reads the config at path if given (the file must exist), else the
// default ~/.config/linqode/config.toml (a missing file is an empty config).
func Load(path string) (*Config, error) {
	required := path != ""
	if !required {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot locate config: %w", err)
		}
		path = filepath.Join(home, ".config", "linqode", "config.toml")
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}
	cfg, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func parse(raw []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	for name, host := range cfg.Hosts {
		if host.Host == "" {
			return nil, fmt.Errorf(`hosts.%s: missing "host"`, name)
		}
	}
	return &cfg, nil
}

// Select picks the host to connect to. arg is a config entry name, an inline
// spec, or empty (allowed only with exactly one configured host).
func (c *Config) Select(arg string) (Selection, error) {
	if arg != "" {
		if host, ok := c.Hosts[arg]; ok {
			return selection(host), nil
		}
		// Not a configured name: treat as an inline host spec.
		return Selection{Spec: arg, HostMetrics: true}, nil
	}
	switch len(c.Hosts) {
	case 1:
		for _, host := range c.Hosts {
			return selection(host), nil
		}
		panic("unreachable")
	case 0:
		return Selection{}, errors.New(
			"no host given and no hosts configured; " +
				"run `linqode user@host` or add a host to the config file")
	default:
		names := slices.Sorted(maps.Keys(c.Hosts))
		return Selection{}, fmt.Errorf(
			"no host given and multiple hosts configured; pick one of: %s",
			strings.Join(names, ", "))
	}
}

func selection(host Host) Selection {
	s := Selection{
		Spec:        host.Host,
		ComposeDir:  host.ComposeDir,
		HostMetrics: host.HostMetrics == nil || *host.HostMetrics,
	}
	for name, command := range host.Scripts {
		s.Scripts = append(s.Scripts, Script{Name: name, Command: command})
	}
	slices.SortFunc(s.Scripts, func(a, b Script) int { return strings.Compare(a.Name, b.Name) })
	return s
}

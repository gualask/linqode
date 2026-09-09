// Package operations owns the application workflows shared by Linqode's
// human and machine interfaces.
package operations

import (
	"fmt"
	"maps"
	"slices"

	"github.com/gualask/linqode/internal/config"
)

// Catalog exposes only capabilities declared in Linqode's configuration.
// Machine callers can select a connection target, but never fall back to an
// inline SSH specification.
type Catalog struct {
	config *config.Config
}

// ConfiguredHost is the connection and project configuration authorized for
// one machine-facing host name. Script bodies remain private to operations.
type ConfiguredHost struct {
	Name        string
	Spec        string
	ComposeDir  string
	HostMetrics bool
	scripts     map[string]string
}

func NewCatalog(cfg *config.Config) Catalog {
	return Catalog{config: cfg}
}

// HostNames returns configured host names in stable order, less the local
// ones. This command is the machine surface's discovery, and listing a name
// every other command here refuses would be a lie.
func (c Catalog) HostNames() []string {
	if c.config == nil {
		return []string{}
	}
	names := make([]string, 0, len(c.config.Hosts))
	for name, configured := range c.config.Hosts {
		if configured.Host == config.LocalSpec {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ScriptNames returns the configured script names for host in stable order.
// Host selection is strict: an unknown name is never treated as an inline SSH
// target.
func (c Catalog) ScriptNames(host string) ([]string, error) {
	configured, err := c.configuredHost(host)
	if err != nil {
		return nil, err
	}
	return sortedKeys(configured.Scripts), nil
}

// SelectHost returns only an exact configured name. Unlike config.Select,
// unknown values are not interpreted as inline SSH targets.
func (c Catalog) SelectHost(name string) (ConfiguredHost, error) {
	configured, err := c.configuredHost(name)
	if err != nil {
		return ConfiguredHost{}, err
	}
	return ConfiguredHost{
		Name:        name,
		Spec:        configured.Host,
		ComposeDir:  configured.ComposeDir,
		HostMetrics: configured.HostMetrics == nil || *configured.HostMetrics,
		scripts:     maps.Clone(configured.Scripts),
	}, nil
}

// RequireScript validates a configured script name without exposing its
// command body. Composition roots can therefore reject it before dialing.
func (h ConfiguredHost) RequireScript(name string) error {
	_, err := configuredScript(h.scripts, name)
	return err
}

func (c Catalog) configuredHost(name string) (config.Host, error) {
	if c.config == nil {
		return config.Host{}, UnknownHostError{Name: name}
	}
	configured, ok := c.config.Hosts[name]
	if !ok {
		return config.Host{}, UnknownHostError{Name: name}
	}
	if configured.Host == config.LocalSpec {
		return config.Host{}, LocalHostError{Name: name}
	}
	return configured, nil
}

func sortedKeys[V any](values map[string]V) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// LocalHostError reports a configured host the machine interface will not
// reach. The boundary's value is the gap between what an agent can do
// without Linqode — nothing on that server — and what Linqode grants it:
// typed operations, and only those. On the machine Linqode runs on that gap
// is zero, because the agent already has a shell there.
//
// So `config.toml` holds two categories of host: those an agent may reach,
// and those only the operator may.
type LocalHostError struct {
	Name string
}

func (e LocalHostError) Error() string {
	return fmt.Sprintf(
		"host %q is local; an agent needs no Linqode to run commands on this machine", e.Name)
}

// UnknownHostError reports a name outside the configured machine boundary.
type UnknownHostError struct {
	Name string
}

func (e UnknownHostError) Error() string {
	return fmt.Sprintf("host %q is not configured", e.Name)
}

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

// HostNames returns configured host names in stable order.
func (c Catalog) HostNames() []string {
	if c.config == nil {
		return []string{}
	}
	return sortedKeys(c.config.Hosts)
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

// UnknownHostError reports a name outside the configured machine boundary.
type UnknownHostError struct {
	Name string
}

func (e UnknownHostError) Error() string {
	return fmt.Sprintf("host %q is not configured", e.Name)
}

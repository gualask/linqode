package operations

import (
	"errors"
	"slices"
	"testing"

	"github.com/gualask/linqode/internal/config"
)

func TestCatalogNamesAreSorted(t *testing.T) {
	catalog := NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"zeta":  {Host: "z", Scripts: map[string]string{"memory": "free -m", "disk": "df -h"}},
		"alpha": {Host: "a"},
	}})

	if got, want := catalog.HostNames(), []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("HostNames() = %v, want %v", got, want)
	}
	if got, want := mustScriptNames(t, catalog, "zeta"), []string{"disk", "memory"}; !slices.Equal(got, want) {
		t.Fatalf("ScriptNames() = %v, want %v", got, want)
	}
}

func TestCatalogReturnsEmptyListsInsteadOfNil(t *testing.T) {
	catalog := NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"empty": {Host: "example"},
	}})

	if got := catalog.HostNames(); got == nil {
		t.Fatal("HostNames() returned nil")
	}
	if got := mustScriptNames(t, catalog, "empty"); got == nil {
		t.Fatal("ScriptNames() returned nil")
	}
}

func TestCatalogRejectsUnknownHost(t *testing.T) {
	catalog := NewCatalog(&config.Config{})
	for _, selectUnknown := range []func() error{
		func() error { _, err := catalog.ScriptNames("user@example.com"); return err },
		func() error { _, err := catalog.SelectHost("user@example.com"); return err },
	} {
		err := selectUnknown()
		var unknown UnknownHostError
		if !errors.As(err, &unknown) {
			t.Fatalf("selection error = %v, want UnknownHostError", err)
		}
		if unknown.Name != "user@example.com" {
			t.Fatalf("unknown host = %q", unknown.Name)
		}
	}
}

// A local host is configured and still outside this boundary: the agent
// already has a shell on the machine Linqode runs on, so the typed
// operations grant it nothing it did not have. The refusal says that rather
// than claiming the name is unknown, which would send a caller hunting for a
// typo.
func TestCatalogRejectsAConfiguredLocalHost(t *testing.T) {
	catalog := NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"laptop":  {Host: config.LocalSpec, Scripts: map[string]string{"build": "make"}},
		"remote":  {Host: "deploy@example.com"},
		"aliased": {Host: "production"},
	}})

	if got, want := catalog.HostNames(), []string{"aliased", "remote"}; !slices.Equal(got, want) {
		t.Errorf("HostNames() = %v, want %v", got, want)
	}
	for _, selectLocal := range []func() error{
		func() error { _, err := catalog.ScriptNames("laptop"); return err },
		func() error { _, err := catalog.SelectHost("laptop"); return err },
	} {
		err := selectLocal()
		var local LocalHostError
		if !errors.As(err, &local) {
			t.Fatalf("selection error = %v, want LocalHostError", err)
		}
		if local.Name != "laptop" {
			t.Errorf("local host = %q", local.Name)
		}
	}
}

func TestCatalogSelectHostReturnsAuthorizedConnectionData(t *testing.T) {
	disabled := false
	catalog := NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"default-metrics": {Host: "default-alias", ComposeDir: "/srv/default"},
		"no-metrics":      {Host: "restricted-alias", ComposeDir: "/srv/restricted", HostMetrics: &disabled},
	}})

	configured, err := catalog.SelectHost("default-metrics")
	if err != nil {
		t.Fatal(err)
	}
	if configured.Name != "default-metrics" || configured.Spec != "default-alias" ||
		configured.ComposeDir != "/srv/default" || !configured.HostMetrics {
		t.Fatalf("configured host = %+v", configured)
	}

	restricted, err := catalog.SelectHost("no-metrics")
	if err != nil {
		t.Fatal(err)
	}
	if restricted.HostMetrics {
		t.Fatalf("host metrics unexpectedly enabled: %+v", restricted)
	}
}

func TestConfiguredHostOperatorCarriesScriptsWithoutExposingBodies(t *testing.T) {
	catalog := NewCatalog(&config.Config{Hosts: map[string]config.Host{
		"production": {
			Host: "production-alias",
			Scripts: map[string]string{
				"deploy": "deploy.sh --yes",
				"backup": "/opt/bin/backup",
			},
		},
	}})
	configured, err := catalog.SelectHost("production")
	if err != nil {
		t.Fatal(err)
	}
	operator := NewConfiguredHostOperator(&fakeExecutor{}, configured)
	if len(operator.scripts) != 2 || operator.scripts["backup"] != "/opt/bin/backup" ||
		operator.scripts["deploy"] != "deploy.sh --yes" {
		t.Fatalf("configured scripts = %#v", operator.scripts)
	}
	if err := configured.RequireScript("deploy"); err != nil {
		t.Fatalf("RequireScript(deploy) = %v", err)
	}
	if err := configured.RequireScript("missing"); err == nil {
		t.Fatal("RequireScript(missing) unexpectedly succeeded")
	} else {
		var unknown UnknownScriptError
		if !errors.As(err, &unknown) || unknown.Name != "missing" {
			t.Fatalf("RequireScript(missing) = %v", err)
		}
	}
}

func mustScriptNames(t *testing.T, catalog Catalog, host string) []string {
	t.Helper()
	names, err := catalog.ScriptNames(host)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

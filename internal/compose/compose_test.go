package compose

import "testing"

func TestPsWithoutDirRunsInDefaultDirectory(t *testing.T) {
	if got := PsCommand(""); got != "docker compose ps --all --format json" {
		t.Errorf("got %q", got)
	}
}

func TestPsWithDirChangesDirectoryFirst(t *testing.T) {
	want := "cd '/srv/myapp' && docker compose ps --all --format json"
	if got := PsCommand("/srv/myapp"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestActionsTargetOneService(t *testing.T) {
	if got := ActionCommand("/srv/myapp", ActionRestart, "web"); got != "cd '/srv/myapp' && docker compose restart 'web'" {
		t.Errorf("got %q", got)
	}
	if got := ActionCommand("", ActionStop, "web"); got != "docker compose stop 'web'" {
		t.Errorf("got %q", got)
	}
	if got := ActionCommand("", ActionStart, "a b"); got != "docker compose start 'a b'" {
		t.Errorf("got %q", got)
	}
}

func TestLogsFollowsOneService(t *testing.T) {
	want := "cd '/srv/myapp' && docker compose logs --follow --no-color --no-log-prefix --tail 200 'web'"
	if got := LogsCommand("/srv/myapp", "web", 200); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestQuotesHostileDirectories(t *testing.T) {
	want := `cd '/srv/it'\''s; rm -rf $HOME' && docker compose ps --all --format json`
	if got := PsCommand("/srv/it's; rm -rf $HOME"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Captured from compose v2.27 (NDJSON, trimmed to relevant fields plus a
// few we ignore).
const ndjson = `
{"Command":"\"docker-entrypoint.sh postgres\"","CreatedAt":"2026-07-20 10:00:00 +0000 UTC","ExitCode":0,"Health":"healthy","ID":"0123456789ab","Image":"postgres:16","Name":"myapp-db-1","Project":"myapp","Publishers":[{"URL":"0.0.0.0","TargetPort":5432,"PublishedPort":5432,"Protocol":"tcp"},{"URL":"::","TargetPort":5432,"PublishedPort":5432,"Protocol":"tcp"}],"RunningFor":"2 hours ago","Service":"db","State":"running","Status":"Up 2 hours (healthy)"}
{"Command":"\"/app/server\"","CreatedAt":"2026-07-20 10:00:01 +0000 UTC","ExitCode":1,"Health":"","ID":"ba9876543210","Image":"myapp/web:latest","Name":"myapp-web-1","Project":"myapp","Publishers":null,"RunningFor":"","Service":"web","State":"exited","Status":"Exited (1) 5 minutes ago"}
`

func TestParsesNDJSONLines(t *testing.T) {
	services, err := ParsePS([]byte(ndjson))
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("got %d services", len(services))
	}

	db := services[0]
	if db.Service != "db" || db.Name != "myapp-db-1" || db.State != "running" ||
		db.Health != "healthy" || db.ExitCode != 0 {
		t.Errorf("db: %+v", db)
	}
	if got := db.PortsSummary(); got != "5432->5432/tcp" {
		t.Errorf("db ports %q", got)
	}

	web := services[1]
	if web.State != "exited" || web.ExitCode != 1 {
		t.Errorf("web: %+v", web)
	}
	if len(web.Publishers) != 0 { // null in the JSON
		t.Errorf("web publishers: %+v", web.Publishers)
	}
}

func TestParsesLegacyJSONArray(t *testing.T) {
	raw := `[{"Name":"a-web-1","Service":"web","State":"running","Status":"Up"},
	         {"Name":"a-db-1","Service":"db","State":"running","Status":"Up"}]`
	services, err := ParsePS([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by service name regardless of input order.
	if len(services) != 2 || services[0].Service != "db" || services[1].Service != "web" {
		t.Errorf("got %+v", services)
	}
}

func TestEmptyOutputMeansNoServices(t *testing.T) {
	for _, raw := range []string{"", "  \n"} {
		services, err := ParsePS([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if len(services) != 0 {
			t.Errorf("ParsePS(%q) = %+v", raw, services)
		}
	}
}

func TestRejectsNonJSONOutput(t *testing.T) {
	if _, err := ParsePS([]byte("NAME  STATUS\nweb   Up")); err == nil {
		t.Error("should reject non-JSON output")
	}
}

func TestPortsSummaryCollapsesIPv4IPv6Duplicates(t *testing.T) {
	service := Service{Publishers: []Publisher{
		{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"},
		{URL: "::", TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"},
		{TargetPort: 5432, Protocol: "tcp"},
	}}
	if got := service.PortsSummary(); got != "8080->80/tcp, 5432/tcp" {
		t.Errorf("got %q", got)
	}
	empty := Service{}
	if got := empty.PortsSummary(); got != "" {
		t.Errorf("empty service ports %q", got)
	}
}

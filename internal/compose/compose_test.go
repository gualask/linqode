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
	if got := LogsCommand("/srv/myapp", "web", 200, true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	want = "cd '/srv/myapp' && docker compose logs --no-color --no-log-prefix --tail 200 'web'"
	if got := LogsCommand("/srv/myapp", "web", 200, false); got != want {
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

func TestInspectNamesTheContainersDirectly(t *testing.T) {
	want := "docker inspect --format '{{.Name}} {{.RestartCount}} {{.State.Pid}}' " +
		"'myapp-db-1' 'myapp-web-1'"
	if got := InspectCommand([]string{"myapp-db-1", "myapp-web-1"}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Nothing to inspect must not produce a command that inspects everything.
	if got := InspectCommand(nil); got != "" {
		t.Errorf("empty list built %q", got)
	}
	want = "docker inspect --format '{{.Name}} {{.RestartCount}} {{.State.Pid}}' " +
		`'a'\''; rm -rf $HOME'`
	if got := InspectCommand([]string{"a'; rm -rf $HOME"}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseInspectedStripsTheLeadingSlash(t *testing.T) {
	found := ParseInspected([]byte("/myapp-db-1 0 4242\n/myapp-web-1 7 0\n"))
	if len(found) != 2 || found["myapp-db-1"] != (Inspected{Restarts: 0, Pid: 4242}) ||
		found["myapp-web-1"] != (Inspected{Restarts: 7}) {
		t.Errorf("got %+v", found)
	}
}

// A container that disappears between `ps` and `inspect` makes the command
// fail and print an error line, while the containers that are still there
// report normally. That partial reading is worth keeping.
func TestParseInspectedSkipsUnparseableLines(t *testing.T) {
	raw := []byte("Error: No such object: myapp-gone-1\n/myapp-web-1 3 99\n\nrubbish\n/x notanumber\n")
	found := ParseInspected(raw)
	if len(found) != 1 || found["myapp-web-1"] != (Inspected{Restarts: 3, Pid: 99}) {
		t.Errorf("got %+v", found)
	}
	if got := ParseInspected(nil); len(got) != 0 {
		t.Errorf("no output should yield nothing, got %+v", got)
	}
}

func TestApplyRestartsMatchesOnContainerName(t *testing.T) {
	services := []Service{
		{Service: "db", Name: "myapp-db-1"},
		{Service: "web", Name: "myapp-web-1"},
	}
	ApplyRestarts(services, map[string]int{"myapp-db-1": 0})

	if services[0].Restarts == nil || *services[0].Restarts != 0 {
		t.Errorf("db restarts %v, want 0", services[0].Restarts)
	}
	// A service the inspect said nothing about stays unknown rather than
	// being reported as never restarted.
	if services[1].Restarts != nil {
		t.Errorf("web restarts %v, want unknown", *services[1].Restarts)
	}
	if got := services[0].RestartsText(); got != "0" {
		t.Errorf("known count renders %q", got)
	}
	if got := services[1].RestartsText(); got != "-" {
		t.Errorf("unknown count renders %q", got)
	}
}

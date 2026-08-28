package cli

import "testing"

func TestParseRoutesTUIAndLocalCommands(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		command    Command
		host       string
		service    string
		scriptName string
		tail       int
		follow     bool
		configPath string
	}{
		{name: "implicit tui", command: CommandTUI},
		{name: "legacy host", args: []string{"production"}, command: CommandTUI, host: "production"},
		{name: "legacy inline host", args: []string{"deploy@example.com:2222"}, command: CommandTUI, host: "deploy@example.com:2222"},
		{name: "legacy config", args: []string{"--config", "other.toml", "production"}, command: CommandTUI, host: "production", configPath: "other.toml"},
		{name: "explicit tui", args: []string{"tui", "production"}, command: CommandTUI, host: "production"},
		{name: "explicit tui config", args: []string{"tui", "--config", "other.toml", "production"}, command: CommandTUI, host: "production", configPath: "other.toml"},
		{name: "config before explicit tui", args: []string{"--config", "other.toml", "tui", "production"}, command: CommandTUI, host: "production", configPath: "other.toml"},
		{name: "hosts", args: []string{"hosts"}, command: CommandHosts},
		{name: "scripts", args: []string{"scripts", "production"}, command: CommandScripts, host: "production"},
		{name: "status", args: []string{"status", "production"}, command: CommandStatus, host: "production"},
		{name: "stats", args: []string{"stats", "production"}, command: CommandStats, host: "production"},
		{name: "follow stats", args: []string{"stats", "--follow", "production"}, command: CommandStats, host: "production", follow: true},
		{name: "bounded logs", args: []string{"logs", "production", "web"}, command: CommandLogs, host: "production", service: "web", tail: 200},
		{name: "follow logs", args: []string{"logs", "--tail", "25", "--follow", "production", "web"}, command: CommandLogs, host: "production", service: "web", tail: 25, follow: true},
		{name: "restart", args: []string{"restart", "production", "web"}, command: CommandRestart, host: "production", service: "web"},
		{name: "stop", args: []string{"stop", "production", "worker"}, command: CommandStop, host: "production", service: "worker"},
		{name: "start", args: []string{"start", "production", "worker"}, command: CommandStart, host: "production", service: "worker"},
		{name: "script", args: []string{"script", "production", "deploy"}, command: CommandScript, host: "production", scriptName: "deploy"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, failure := Parse(test.args)
			if failure != nil {
				t.Fatalf("Parse() failure = %v", failure)
			}
			if got.Command != test.command || got.Host != test.host || got.Service != test.service ||
				got.ScriptName != test.scriptName || got.Tail != test.tail || got.Follow != test.follow ||
				got.ConfigPath != test.configPath {
				t.Fatalf("Parse() = %+v", got)
			}
		})
	}
}

func TestParseRejectsInvalidBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		kind      string
		operation string
	}{
		{name: "removed exec", args: []string{"--exec", "date"}, kind: "invalid_option", operation: "command"},
		{name: "config before machine command", args: []string{"--config", "other.toml", "hosts"}, kind: "invalid_option", operation: "hosts"},
		{name: "config after machine command", args: []string{"scripts", "--config", "other.toml", "production"}, kind: "invalid_option", operation: "scripts"},
		{name: "legacy extra operand", args: []string{"production", "extra"}, kind: "usage", operation: "tui"},
		{name: "explicit tui extra operand", args: []string{"tui", "production", "extra"}, kind: "usage", operation: "tui"},
		{name: "hosts operand", args: []string{"hosts", "production"}, kind: "usage", operation: "hosts"},
		{name: "scripts missing host", args: []string{"scripts"}, kind: "usage", operation: "scripts"},
		{name: "scripts runtime argument", args: []string{"scripts", "production", "extra"}, kind: "usage", operation: "scripts"},
		{name: "status missing host", args: []string{"status"}, kind: "usage", operation: "status"},
		{name: "status extra operand", args: []string{"status", "production", "extra"}, kind: "usage", operation: "status"},
		{name: "stats missing host", args: []string{"stats", "--follow"}, kind: "usage", operation: "stats"},
		{name: "stats unknown option", args: []string{"stats", "--interval", "1", "production"}, kind: "invalid_option", operation: "stats"},
		{name: "logs missing service", args: []string{"logs", "production"}, kind: "usage", operation: "logs"},
		{name: "logs zero tail", args: []string{"logs", "--tail", "0", "production", "web"}, kind: "invalid_argument", operation: "logs"},
		{name: "logs excessive tail", args: []string{"logs", "--tail", "10001", "production", "web"}, kind: "invalid_argument", operation: "logs"},
		{name: "logs invalid tail", args: []string{"logs", "--tail", "many", "production", "web"}, kind: "invalid_option", operation: "logs"},
		{name: "logs option after operands", args: []string{"logs", "production", "web", "--follow"}, kind: "usage", operation: "logs"},
		{name: "restart missing service", args: []string{"restart", "production"}, kind: "usage", operation: "restart"},
		{name: "stop extra operand", args: []string{"stop", "production", "web", "extra"}, kind: "usage", operation: "stop"},
		{name: "start option", args: []string{"start", "--force", "production", "web"}, kind: "invalid_option", operation: "start"},
		{name: "script missing name", args: []string{"script", "production"}, kind: "usage", operation: "script"},
		{name: "script runtime argument", args: []string{"script", "production", "deploy", "--force"}, kind: "usage", operation: "script"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure := Parse(test.args)
			if failure == nil {
				t.Fatal("Parse() unexpectedly succeeded")
			}
			if failure.Kind != test.kind || failure.Operation != test.operation || failure.ExitCode != 2 {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}

func TestParseReservesCommandNamesForMachineRoutes(t *testing.T) {
	validArgs := map[Command][]string{
		CommandHosts:   nil,
		CommandScripts: {"production"},
		CommandStatus:  {"production"},
		CommandStats:   {"production"},
		CommandLogs:    {"production", "web"},
		CommandRestart: {"production", "web"},
		CommandStop:    {"production", "web"},
		CommandStart:   {"production", "web"},
		CommandScript:  {"production", "deploy"},
	}
	for name, command := range machineCommands {
		t.Run(name, func(t *testing.T) {
			got, failure := Parse(append([]string{name}, validArgs[command]...))
			if failure != nil || got.Command != command {
				t.Fatalf("route = %+v, failure %v", got, failure)
			}
		})
	}

	got, failure := Parse([]string{"tui", "status"})
	if failure != nil || got.Command != CommandTUI || got.Host != "status" {
		t.Fatalf("explicit reserved host route = %+v, failure %v", got, failure)
	}
}

func TestParseHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"help", "scripts"}, {"scripts", "--help"}} {
		got, failure := Parse(args)
		if failure != nil || got.Command != CommandHelp {
			t.Fatalf("Parse(%v) = %+v, failure %v", args, got, failure)
		}
	}
}

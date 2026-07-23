// Package compose builds the remote `docker compose` commands and parses
// their output into typed models.
package compose

import "strings"

// shellQuote quotes s as a single POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// inDir prefixes command with a `cd` into the compose directory, when one
// is configured (the caller's remote working directory otherwise).
func inDir(composeDir, command string) string {
	if composeDir == "" {
		return command
	}
	return "cd " + shellQuote(composeDir) + " && " + command
}

// PsCommand builds the remote command listing all services of the compose
// project in composeDir (the caller's remote working directory when empty).
//
// `--format json` is NDJSON on compose >= 2.21 and a JSON array before
// that; ParsePS accepts both.
func PsCommand(composeDir string) string {
	return inDir(composeDir, "docker compose ps --all --format json")
}

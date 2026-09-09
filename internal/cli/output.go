package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

const schemaVersion = 1

type catalog interface {
	HostNames() []string
	ScriptNames(string) ([]string, error)
}

type namedItem struct {
	Name string `json:"name"`
}

type hostsDocument struct {
	SchemaVersion int         `json:"schema_version"`
	Hosts         []namedItem `json:"hosts"`
}

type scriptsDocument struct {
	SchemaVersion int         `json:"schema_version"`
	Host          string      `json:"host"`
	Scripts       []namedItem `json:"scripts"`
}

type errorDocument struct {
	SchemaVersion int          `json:"schema_version"`
	Error         errorPayload `json:"error"`
}

type errorPayload struct {
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	Operation string `json:"operation"`
}

// Failure is a typed machine-interface failure and its process exit code.
type Failure struct {
	Kind      string
	Message   string
	Operation string
	ExitCode  int
}

// OperationalFailure classifies a Linqode failure after valid input.
func OperationalFailure(operation, kind string, err error) *Failure {
	return &Failure{Kind: kind, Message: err.Error(), Operation: operation, ExitCode: 1}
}

func inputFailure(operation, kind, message string) *Failure {
	return &Failure{Kind: kind, Message: message, Operation: operation, ExitCode: 2}
}

// Run executes commands that need only the local configured catalog.
func Run(invocation Invocation, configured catalog, stdout, stderr io.Writer) int {
	switch invocation.Command {
	case CommandHosts:
		names := configured.HostNames()
		return writeSuccess(stdout, stderr, invocation.Operation(), hostsDocument{
			SchemaVersion: schemaVersion,
			Hosts:         namedItems(names),
		})
	case CommandScripts:
		names, err := configured.ScriptNames(invocation.Host)
		if err != nil {
			return Report(stderr, SelectionFailure(invocation.Operation(), err))
		}
		return writeSuccess(stdout, stderr, invocation.Operation(), scriptsDocument{
			SchemaVersion: schemaVersion,
			Host:          invocation.Host,
			Scripts:       namedItems(names),
		})
	default:
		return Report(stderr, OperationalFailure(invocation.Operation(), "unsupported_operation",
			fmt.Errorf("%s is not a local catalog operation", invocation.Command)))
	}
}

// Report emits one JSON error document and returns its process exit code.
func Report(stderr io.Writer, failure *Failure) int {
	if err := writeJSON(stderr, errorDocument{
		SchemaVersion: schemaVersion,
		Error: errorPayload{
			Kind:      failure.Kind,
			Message:   failure.Message,
			Operation: failure.Operation,
		},
	}); err != nil {
		return 1
	}
	return failure.ExitCode
}

func writeSuccess(stdout, stderr io.Writer, operation string, value any) int {
	if err := writeJSON(stdout, value); err != nil {
		return Report(stderr, OperationalFailure(operation, "output_error", err))
	}
	return 0
}

func writeJSON(w io.Writer, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func namedItems(names []string) []namedItem {
	items := make([]namedItem, 0, len(names))
	for _, name := range names {
		items = append(items, namedItem{Name: name})
	}
	return items
}

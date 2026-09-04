package home

// What the screen asks the application to open. These are requests, not
// results: the home model knows what the operator asked for and nothing about
// SSH, and the application turns each into an operation feed and a view.

import "github.com/gualask/linqode/internal/operations"

type OpenLogsMsg struct {
	Title   string
	Service string
}

type OpenActionMsg struct {
	Title   string
	Service string
	Action  operations.ServiceAction
}

type OpenScriptMsg struct {
	Title string
	Name  string
}

type OpenAdHocMsg struct {
	Title   string
	Command string
}

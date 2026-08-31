package status

import (
	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
)

// Config supplies the status view's static context and background operations.
type Config struct {
	Target        string
	ComposeDir    string
	Scripts       []operations.Script
	Services      func() ([]compose.Service, error)
	Host          func() (host.Metrics, error)
	Stats         func() ([]compose.ContainerStats, error)
	LiveStats     bool
	ActionPreview func(operations.ServiceAction, string) string
}

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

type OpenStatsMsg struct{}

type StatsFeedMsg struct {
	Feed operations.Feed
	Err  error
}

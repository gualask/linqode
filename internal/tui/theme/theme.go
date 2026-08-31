// Package theme owns the visual tokens shared by TUI features.
package theme

import "github.com/charmbracelet/lipgloss"

var (
	Bold    = lipgloss.NewStyle().Bold(true)
	Dim     = lipgloss.NewStyle().Faint(true)
	Cyan    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	Red     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	Green   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	Yellow  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	Blue    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	Magenta = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	Reverse = lipgloss.NewStyle().Reverse(true)
	Match   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))
)

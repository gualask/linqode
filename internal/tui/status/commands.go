package status

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/theme"
)

type menuEntry struct {
	label   string
	detail  string
	request tea.Msg
}

type menu struct {
	entries  []menuEntry
	selected int
	key      string
}

func (m *Model) move(delta int) {
	if len(m.services) == 0 {
		return
	}
	m.selected = min(max(m.selected+delta, 0), len(m.services)-1)
}

func openRequest(message tea.Msg) tea.Cmd {
	return func() tea.Msg { return message }
}

func openLogs(title, service string) tea.Cmd {
	return openRequest(OpenLogsMsg{Title: title, Service: service})
}

func (m *Model) openActionMenu() {
	if m.selected >= len(m.services) {
		return
	}
	service := m.services[m.selected].Service
	actions := []operations.ServiceAction{
		operations.ActionRestart, operations.ActionStop, operations.ActionStart,
	}
	entries := make([]menuEntry, len(actions))
	for index, action := range actions {
		detail := action.Verb() + " " + service
		if m.actionPreview != nil {
			detail = m.actionPreview(action, service)
		}
		entries[index] = menuEntry{
			label: action.Verb() + " " + service, detail: detail,
			request: OpenActionMsg{
				Title: action.Verb() + ": " + service, Service: service, Action: action,
			},
		}
	}
	m.menu = &menu{entries: entries, key: "c"}
}

func (m *Model) openScriptMenu() {
	if len(m.info.Scripts) == 0 {
		m.errText = "no scripts configured for this host"
		return
	}
	entries := make([]menuEntry, len(m.info.Scripts))
	for index, script := range m.info.Scripts {
		entries[index] = menuEntry{
			label: script.Name, detail: script.Command,
			request: OpenScriptMsg{Title: "script: " + script.Name, Name: script.Name},
		}
	}
	m.menu = &menu{entries: entries, key: "x"}
}

func (m *Model) handleCommandKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		m.commandPrompt, m.commandText = false, ""
	case "enter":
		return m.submitAdHocCommand()
	case "backspace":
		if runes := []rune(m.commandText); len(runes) > 0 {
			m.commandText = string(runes[:len(runes)-1])
		}
	default:
		if len(key.Runes) > 0 {
			m.commandText += string(key.Runes)
		}
	}
	return nil
}

func (m *Model) submitAdHocCommand() tea.Cmd {
	command := strings.TrimSpace(m.commandText)
	m.commandPrompt, m.commandText = false, ""
	if command == "" {
		return nil
	}
	m.lastCommand = command
	return openRequest(OpenAdHocMsg{Title: "$ " + command, Command: command})
}

func (m *Model) handleMenuKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", m.menu.key:
		m.menu = nil
	case "j", "down":
		m.menu.selected = min(m.menu.selected+1, len(m.menu.entries)-1)
	case "k", "up":
		m.menu.selected = max(m.menu.selected-1, 0)
	case "enter":
		entry := m.menu.entries[m.menu.selected]
		m.menu = nil
		return openRequest(entry.request)
	}
	return nil
}

func (m *Model) renderMenu(width, height int) string {
	labelWidth := 0
	for _, entry := range m.menu.entries {
		labelWidth = max(labelWidth, len(entry.label))
	}
	lines := make([]string, 0, len(m.menu.entries))
	for index, entry := range m.menu.entries {
		line := fmt.Sprintf(" %-*s  ", labelWidth, entry.label)
		if index == m.menu.selected {
			lines = append(lines, theme.Reverse.Render(line+entry.detail+" "))
		} else {
			lines = append(lines, theme.Bold.Render(line)+theme.Dim.Render(entry.detail+" "))
		}
	}
	view := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	if width > 0 && height > 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, view)
	}
	return view
}

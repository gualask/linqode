package tui

// Compose status view: the project's services in a table, refreshed
// manually with `r` and automatically on an interval. A failed refresh
// shows its error in the footer while the last good table stays on screen.
// Enter opens the log view for the selected service.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
)

const autoRefresh = 5 * time.Second

// logTail is how many lines of history `docker compose logs` starts with.
const logTail = 200

// servicesMsg is the outcome of a refresh, delivered asynchronously so the
// UI never blocks on the SSH round-trip.
type servicesMsg struct {
	services []compose.Service
	err      error
}

type autoTickMsg struct{}

type statusModel struct {
	info  Info
	fetch Fetch

	services []compose.Service
	selected int
	// errText is the last refresh failure; the previous service list stays
	// on screen.
	errText    string
	loaded     bool // first refresh done (either way)
	refreshing bool

	// scriptMenu is the selected index in the scripts menu; -1 when the
	// menu is closed. While open, keys route to the menu.
	scriptMenu int

	width, height int
}

func newStatusModel(info Info, fetch Fetch) statusModel {
	return statusModel{info: info, fetch: fetch, scriptMenu: -1}
}

// init returns the startup commands. It must not mutate state: Bubble Tea
// calls Init on a copy whose changes are discarded.
func (m *statusModel) init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), autoTick())
}

func (m *statusModel) refreshCmd() tea.Cmd {
	fetch := m.fetch
	return func() tea.Msg {
		services, err := fetch()
		return servicesMsg{services: services, err: err}
	}
}

// refresh starts a fetch unless one is already running.
func (m *statusModel) refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	return m.refreshCmd()
}

func autoTick() tea.Cmd {
	return tea.Tick(autoRefresh, func(time.Time) tea.Msg { return autoTickMsg{} })
}

func (m *statusModel) setSize(width, height int) {
	m.width, m.height = width, height
}

func (m *statusModel) setError(text string) {
	m.errText = text
}

func (m *statusModel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case servicesMsg:
		m.refreshing = false
		m.loaded = true
		if msg.err != nil {
			m.errText = msg.err.Error()
			return nil
		}
		// Keep the cursor on the same service across refreshes; if it is
		// gone, stay at the same position, clamped into range.
		if m.selected < len(m.services) {
			name := m.services[m.selected].Name
			for i, s := range msg.services {
				if s.Name == name {
					m.selected = i
					break
				}
			}
		}
		m.services = msg.services
		m.selected = min(m.selected, max(0, len(m.services)-1))
		m.errText = ""

	case autoTickMsg:
		return tea.Batch(m.refresh(), autoTick())

	case tea.KeyMsg:
		if m.scriptMenu >= 0 {
			return m.handleScriptMenuKey(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "g", "home":
			m.selected = 0
		case "G", "end":
			m.selected = max(0, len(m.services)-1)
		case "r":
			return m.refresh()
		case "enter", "l":
			if m.selected < len(m.services) {
				service := m.services[m.selected].Service
				return openFollow("logs: "+service,
					compose.LogsCommand(m.info.ComposeDir, service, logTail))
			}
		case "R":
			return m.action(compose.ActionRestart)
		case "s":
			return m.action(compose.ActionStop)
		case "S":
			return m.action(compose.ActionStart)
		case "x":
			if len(m.info.Scripts) == 0 {
				m.errText = "no scripts configured for this host"
			} else {
				m.scriptMenu = 0
			}
		}
	}
	return nil
}

func openFollow(title, command string) tea.Cmd {
	msg := openFollowMsg{title: title, command: command}
	return func() tea.Msg { return msg }
}

// action runs a compose lifecycle action on the selected service.
func (m *statusModel) action(action compose.ServiceAction) tea.Cmd {
	if m.selected >= len(m.services) {
		return nil
	}
	service := m.services[m.selected].Service
	return openFollow(action.Verb()+": "+service,
		compose.ActionCommand(m.info.ComposeDir, action, service))
}

func (m *statusModel) handleScriptMenuKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "x":
		m.scriptMenu = -1
	case "j", "down":
		m.scriptMenu = min(m.scriptMenu+1, len(m.info.Scripts)-1)
	case "k", "up":
		m.scriptMenu = max(m.scriptMenu-1, 0)
	case "enter":
		script := m.info.Scripts[m.scriptMenu]
		m.scriptMenu = -1
		return openFollow("script: "+script.Name, script.Command)
	}
	return nil
}

func (m *statusModel) move(delta int) {
	if len(m.services) == 0 {
		return
	}
	m.selected = min(max(m.selected+delta, 0), len(m.services)-1)
}

var (
	boldStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	cyanStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	blueStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	magentaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	reverseStyle = lipgloss.NewStyle().Reverse(true)
	matchStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))
)

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return greenStyle
	case "restarting", "paused", "created":
		return yellowStyle
	case "exited", "dead":
		return redStyle
	default:
		return lipgloss.NewStyle()
	}
}

func healthStyle(health string) lipgloss.Style {
	switch health {
	case "":
		return dimStyle
	case "healthy":
		return greenStyle
	case "starting":
		return yellowStyle
	case "unhealthy":
		return redStyle
	default:
		return lipgloss.NewStyle()
	}
}

func (m *statusModel) view() string {
	var b strings.Builder

	// Header: target and compose dir.
	b.WriteString(boldStyle.Render(" linqode "))
	b.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		b.WriteString("  ")
		b.WriteString(cyanStyle.Render(m.info.ComposeDir))
	}
	b.WriteString("\n\n")

	tableHeight := max(m.height-4, 1) // header block (2) + table header (1) + footer (1)
	switch {
	case m.scriptMenu >= 0:
		b.WriteString(m.renderScriptsMenu(tableHeight + 1))
		b.WriteString("\n")
	case len(m.services) == 0 && !m.loaded:
		b.WriteString(dimStyle.Render("  (loading services…)"))
		b.WriteString("\n")
	case len(m.services) == 0 && m.errText != "":
		b.WriteString(dimStyle.Render("  (no data — see error below)"))
		b.WriteString("\n")
	case len(m.services) == 0:
		b.WriteString(dimStyle.Render("  (no services in this compose project)"))
		b.WriteString("\n")
	default:
		m.renderTable(&b, tableHeight)
	}

	// Footer: menu hints, error, or count plus keys.
	b.WriteString("\n")
	switch {
	case m.scriptMenu >= 0:
		b.WriteString(dimStyle.Render(" j/k select · enter run · esc cancel"))
	case m.errText != "":
		b.WriteString(redStyle.Render(" " + strings.ReplaceAll(m.errText, "\n", " · ")))
	default:
		b.WriteString(fmt.Sprintf(" %d services", len(m.services)))
		b.WriteString(dimStyle.Render(
			"  ·  enter logs · R restart · s stop · S start · x scripts · r refresh · q quit"))
	}
	return b.String()
}

// renderScriptsMenu shows the predefined scripts, centered where the table
// normally is.
func (m *statusModel) renderScriptsMenu(height int) string {
	nameWidth := 0
	for _, script := range m.info.Scripts {
		nameWidth = max(nameWidth, len(script.Name))
	}
	var lines []string
	for i, script := range m.info.Scripts {
		line := fmt.Sprintf(" %-*s  ", nameWidth, script.Name)
		if i == m.scriptMenu {
			lines = append(lines, reverseStyle.Render(line+script.Command+" "))
		} else {
			lines = append(lines, boldStyle.Render(line)+dimStyle.Render(script.Command+" "))
		}
	}
	menu := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, menu)
	}
	return menu
}

// renderTable writes the service table, keeping the selection visible when
// there are more rows than fit.
func (m *statusModel) renderTable(b *strings.Builder, height int) {
	widths := m.columnWidths()
	pad := func(s string, w int) string {
		if len(s) > w {
			if w <= 1 {
				return strings.Repeat(".", max(w, 0))
			}
			return s[:w-1] + "…"
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	header := fmt.Sprintf(" %s  %s  %s  %s  %s",
		pad("SERVICE", widths[0]), pad("STATE", widths[1]), pad("HEALTH", widths[2]),
		pad("PORTS", widths[3]), pad("STATUS", widths[4]))
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")

	rows := max(height-1, 1)
	offset := 0
	if m.selected >= rows {
		offset = m.selected - rows + 1
	}
	for i := offset; i < min(len(m.services), offset+rows); i++ {
		s := m.services[i]
		healthText := s.Health
		if healthText == "" {
			healthText = "-"
		}
		service := pad(s.Service, widths[0])
		state := pad(s.State, widths[1])
		health := pad(healthText, widths[2])
		ports := pad(s.PortsSummary(), widths[3])
		status := pad(s.Status, widths[4])
		if i == m.selected {
			// One uniform style for the selected row keeps the highlight
			// readable over the per-cell colors.
			b.WriteString(reverseStyle.Render(fmt.Sprintf(" %s  %s  %s  %s  %s",
				service, state, health, ports, status)))
		} else {
			fmt.Fprintf(b, " %s  %s  %s  %s  %s",
				service, stateStyle(s.State).Render(state),
				healthStyle(s.Health).Render(health), ports, status)
		}
		b.WriteString("\n")
	}
}

// columnWidths sizes SERVICE/STATE/HEALTH/PORTS from their content and
// gives STATUS the rest of the terminal width.
func (m *statusModel) columnWidths() [5]int {
	widths := [5]int{len("SERVICE"), len("STATE"), len("HEALTH"), len("PORTS"), len("STATUS")}
	for _, s := range m.services {
		widths[0] = max(widths[0], len(s.Service))
		widths[1] = max(widths[1], len(s.State))
		widths[2] = max(widths[2], len(s.Health))
		widths[3] = max(widths[3], len(s.PortsSummary()))
	}
	used := 1 + widths[0] + 2 + widths[1] + 2 + widths[2] + 2 + widths[3] + 2
	if m.width > 0 {
		widths[4] = max(widths[4], m.width-used-1)
	} else {
		for _, s := range m.services {
			widths[4] = max(widths[4], len(s.Status))
		}
	}
	return widths
}

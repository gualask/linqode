package tui

// Compose status view: the project's services in a table, refreshed
// manually with `r` and automatically on an interval. A failed refresh
// shows its error in the footer while the last good table stays on screen.
// Enter opens the log view for the selected service.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
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

// hostMsg is one sample of the machine's resource usage, fetched alongside
// the service list on the same tick.
type hostMsg struct {
	metrics host.Metrics
	err     error
}

type statusModel struct {
	info  Info
	fetch Fetch
	// hostFetch is optional: without it the header shows no resource line.
	hostFetch FetchHost

	// metrics is the last good host sample. A failed sample keeps it on
	// screen and flags it stale, the same way a failed service refresh
	// keeps the last good table.
	metrics           host.Metrics
	metricsLoaded     bool
	metricsStale      bool
	metricsRefreshing bool

	// statsFetch is optional: without it the table has no resource columns.
	statsFetch FetchStats

	// stats is the latest reading per container name, keyed to match
	// compose.Service.Name. It comes from the soft sample, or from the live
	// stream while that is running.
	stats           map[string]compose.ContainerStats
	statsLoaded     bool
	statsRefreshing bool
	statsErr        string

	// statsFeed streams `docker stats` while the live panel is open; nil
	// otherwise, so the server samples nothing for a panel nobody is
	// looking at.
	statsFeed     *LogFeed
	statsStarting bool
	// history is the CPU series per container, filled only by the live
	// stream: its samples are a second apart, which is what makes a
	// sparkline mean anything.
	history map[string][]float64

	services []compose.Service
	selected int
	// errText is the last refresh failure; the previous service list stays
	// on screen.
	errText    string
	loaded     bool // first refresh done (either way)
	refreshing bool

	// menu is the open modal list, nil when none is. While one is open,
	// keys route to it instead of to the table.
	menu *menu

	// commandPrompt is the `!` ad-hoc command line. While it is open every
	// key edits the text, so `q` types a q instead of quitting. commandText
	// is what has been typed; lastCommand is what was last run, which the
	// prompt reopens with — the same courtesy `f` does for filters.
	commandPrompt bool
	commandText   string
	lastCommand   string

	width, height int
}

// menuEntry is one line of a modal menu: a label, and the command choosing
// it runs. The command doubles as the dimmed detail column, so a menu always
// shows exactly what it is about to execute.
type menuEntry struct {
	label   string
	command string
	// title labels the log view the command's output streams into.
	title string
}

// menu is a modal list over the table: the service actions on `c`, the
// configured scripts on `x`. Both pick one entry out of a short list and run
// it through the log-follow pipeline, so they share navigation, rendering,
// and footer hints.
type menu struct {
	entries  []menuEntry
	selected int
	// key is the one that opened the menu; pressing it again closes it.
	key string
}

func newStatusModel(info Info, fetch Fetch) statusModel {
	return statusModel{info: info, fetch: fetch}
}

// init returns the startup commands. It must not mutate state: Bubble Tea
// calls Init on a copy whose changes are discarded.
//
// The three fetches run on their own intervals — services and host metrics
// every autoRefresh, container stats every statsPollInterval — so the slow
// one never delays the cheap ones.
func (m *statusModel) init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.hostRefreshCmd(), m.statsSampleCmd(),
		autoTick(), statsPollTick())
}

func (m *statusModel) refreshCmd() tea.Cmd {
	fetch := m.fetch
	return func() tea.Msg {
		services, err := fetch()
		return servicesMsg{services: services, err: err}
	}
}

// hostRefreshCmd samples the host metrics, or nil when none are configured.
// Separate from the service fetch: it is a different command on the server
// (~2 ms against ~60 ms), and one failing must not blank the other.
func (m *statusModel) hostRefreshCmd() tea.Cmd {
	fetch := m.hostFetch
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		metrics, err := fetch()
		return hostMsg{metrics: metrics, err: err}
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

// refreshHost samples host metrics unless a sample is already in flight.
func (m *statusModel) refreshHost() tea.Cmd {
	if m.hostFetch == nil || m.metricsRefreshing {
		return nil
	}
	m.metricsRefreshing = true
	return m.hostRefreshCmd()
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

	case statsSampleMsg:
		m.statsRefreshing = false
		if msg.err != nil {
			// Like a failed host sample: the columns keep their last values
			// and the table's own error reporting stays free for refresh
			// failures, which are the ones worth acting on.
			m.statsErr = msg.err.Error()
			return nil
		}
		m.statsErr = ""
		m.applySample(msg.stats)

	case statsPollMsg:
		return tea.Batch(m.refreshStats(), statsPollTick())

	case statsFeedMsg:
		m.statsStarting = false
		if msg.err != nil {
			m.statsErr = msg.err.Error()
			return nil
		}
		feed := msg.feed
		m.statsFeed = &feed
		if m.stats == nil {
			m.stats = map[string]compose.ContainerStats{}
		}
		return statsTick()

	case statsTickMsg:
		if m.statsFeed == nil {
			return nil
		}
		if ended := m.drainStats(); ended {
			// The stream stopped on its own (the project went away, or
			// docker exited). Keep the last samples on screen but stop
			// ticking for a feed that will never produce again, and let the
			// soft poll take the columns back.
			m.statsFeed.Stop()
			m.statsFeed = nil
			// The history ends with the stream: a later one would append to
			// it across a gap and draw the two as if they were continuous.
			m.history = nil
			return m.refreshStats()
		}
		return statsTick()

	case hostMsg:
		m.metricsRefreshing = false
		if msg.err != nil {
			// Keep the last sample visible, marked stale: a blip in the
			// metrics is not worth clearing the header for, and the service
			// list carries its own error reporting.
			m.metricsStale = true
			return nil
		}
		m.metrics = msg.metrics
		m.metricsLoaded = true
		m.metricsStale = false

	case autoTickMsg:
		return tea.Batch(m.refresh(), m.refreshHost(), autoTick())

	case tea.KeyMsg:
		if m.commandPrompt {
			return m.handleCommandKey(msg)
		}
		if m.menu != nil {
			return m.handleMenuKey(msg)
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
		case "c":
			m.openActionMenu()
		case "x":
			m.openScriptMenu()
		case "!":
			m.commandPrompt, m.commandText = true, m.lastCommand
			m.errText = ""
		case "a":
			return m.toggleLive()
		}
	}
	return nil
}

func openFollow(title, command string) tea.Cmd {
	msg := openFollowMsg{title: title, command: command}
	return func() tea.Msg { return msg }
}

// openActionMenu lists the lifecycle actions for the selected service.
//
// They sit behind a menu rather than getting a key each so that no two of
// them differ only by the shift key: `s` and `S` for stop and start put a
// production service one mistyped capital away from the opposite outcome.
// The menu also shows the exact command before it runs.
func (m *statusModel) openActionMenu() {
	if m.selected >= len(m.services) {
		return
	}
	service := m.services[m.selected].Service
	actions := []compose.ServiceAction{
		compose.ActionRestart, compose.ActionStop, compose.ActionStart,
	}
	entries := make([]menuEntry, len(actions))
	for i, action := range actions {
		entries[i] = menuEntry{
			label:   action.Verb() + " " + service,
			command: compose.ActionCommand(m.info.ComposeDir, action, service),
			title:   action.Verb() + ": " + service,
		}
	}
	m.menu = &menu{entries: entries, key: "c"}
}

// openScriptMenu lists the host's configured scripts.
func (m *statusModel) openScriptMenu() {
	if len(m.info.Scripts) == 0 {
		m.errText = "no scripts configured for this host"
		return
	}
	entries := make([]menuEntry, len(m.info.Scripts))
	for i, script := range m.info.Scripts {
		entries[i] = menuEntry{
			label:   script.Name,
			command: script.Command,
			title:   "script: " + script.Name,
		}
	}
	m.menu = &menu{entries: entries, key: "x"}
}

// handleCommandKey collects the ad-hoc command line, mirroring the log
// view's prompts: esc cancels, enter runs, everything else edits.
//
// The command is sent as typed, with no `cd` into the compose directory —
// what the user writes runs where `ssh host 'command'` would run it, the
// same rule the configured scripts and `--exec` follow. Only the actions
// Linqode builds itself are project-relative.
func (m *statusModel) handleCommandKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		m.commandPrompt, m.commandText = false, ""
	case "enter":
		command := strings.TrimSpace(m.commandText)
		m.commandPrompt, m.commandText = false, ""
		if command == "" {
			return nil
		}
		m.lastCommand = command
		return openFollow("$ "+command, command)
	case "backspace":
		if r := []rune(m.commandText); len(r) > 0 {
			m.commandText = string(r[:len(r)-1])
		}
	default:
		if len(key.Runes) > 0 {
			m.commandText += string(key.Runes)
		}
	}
	return nil
}

// handleMenuKey drives the open menu. The key that opened it closes it, so
// the same finger that reached for it backs out.
func (m *statusModel) handleMenuKey(msg tea.KeyMsg) tea.Cmd {
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
		return openFollow(entry.title, entry.command)
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

// restartStyle colors a restart count. One restart is worth noticing; a
// handful means a container is looping rather than recovering, which is the
// reading this column exists to surface.
func restartStyle(n int) lipgloss.Style {
	switch {
	case n >= 5:
		return redStyle
	case n > 0:
		return yellowStyle
	default:
		return dimStyle
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

// view lays the screen out in three bands: a header, a body, and a footer.
// The body is the table — with the live panel under it when open — beside
// the system panel, when the terminal is wide enough for one.
func (m *statusModel) view() string {
	var b strings.Builder

	// Header: target and compose dir, then the host's resource line when it
	// has nowhere better to be.
	b.WriteString(boldStyle.Render(" linqode "))
	b.WriteString(m.info.Target)
	if m.info.ComposeDir != "" {
		b.WriteString("  ")
		b.WriteString(cyanStyle.Render(m.info.ComposeDir))
	}
	b.WriteString("\n")

	sidebar := m.sidebarOn()
	headerLines := 2 // title + the blank line below the header block
	if !sidebar {
		if line := m.renderHostLine(); line != "" {
			b.WriteString(line)
			b.WriteString("\n")
			headerLines++
		}
	}
	b.WriteString("\n")

	// header block + footer (1)
	bodyHeight := max(m.height-headerLines-1, 1)
	bodyWidth := m.width
	if sidebar {
		bodyWidth = m.width - sidebarWidth
	}

	// fit pins a block to the body's height, so the footer sits at the
	// bottom of the screen rather than under the last table row. Only with a
	// known terminal height: before the first WindowSizeMsg there is nothing
	// to pin to, and clipping to a guessed height would truncate the view.
	fit := func(style lipgloss.Style) lipgloss.Style {
		if m.height <= 0 {
			return style
		}
		return style.Height(bodyHeight).MaxHeight(bodyHeight)
	}

	body := fit(lipgloss.NewStyle()).Render(m.renderBody(bodyWidth, bodyHeight))
	if sidebar {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			fit(lipgloss.NewStyle().Width(bodyWidth)).Render(body),
			fit(lipgloss.NewStyle().Width(sidebarInner)).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("8")).
				PaddingLeft(1).
				Render(m.renderSystemPanel()))
	}
	b.WriteString(body)

	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

// renderBody is everything left of the system panel: the service table, or
// what stands in for it, with the live panel below when it is open.
func (m *statusModel) renderBody(width, height int) string {
	if m.menu != nil {
		return m.renderMenu(width, height)
	}

	live := ""
	tableHeight := height
	if m.liveActive() {
		liveHeight := min(m.liveHeight(), max(height-3, 0))
		if liveHeight > 0 {
			tableHeight = height - liveHeight
			live = "\n" + m.renderLivePanel(width)
		}
	}

	var b strings.Builder
	switch {
	case len(m.services) == 0 && !m.loaded:
		b.WriteString(dimStyle.Render("  (loading services…)"))
	case len(m.services) == 0 && m.errText != "":
		b.WriteString(dimStyle.Render("  (no data — see error below)"))
	case len(m.services) == 0:
		b.WriteString(dimStyle.Render("  (no services in this compose project)"))
	default:
		// The table pays for its own header line.
		m.renderTable(&b, width, tableHeight-1)
	}
	return strings.TrimRight(b.String(), "\n") + live
}

// footerHint is one key hint and how readily it is given up when the
// terminal is too narrow to show them all: the higher the rank, the sooner
// it goes.
type footerHint struct {
	text string
	drop int
}

// footerHints renders the key hints, dropping the least essential ones until
// they fit in width columns (unbounded when width is not positive).
//
// Ranking them beats a tier per terminal width: the way out stays on screen
// at any size, and a new key only has to declare how expendable it is.
func (m *statusModel) footerHints(width int) string {
	live := "a live"
	if m.liveActive() {
		live = "a live off"
	}
	hints := []footerHint{
		{"enter logs", 2},
		{"c actions", 3},
		{"x scripts", 6},
		{"! run", 4},
		{live, 5},
		{"r refresh", 1},
		{"q quit", 0},
	}
	for {
		texts := make([]string, len(hints))
		for i, hint := range hints {
			texts[i] = hint.text
		}
		joined := strings.Join(texts, " · ")
		if width <= 0 || len(hints) == 1 || lipgloss.Width(joined) <= width {
			return joined
		}
		worst := 0
		for i, hint := range hints {
			if hint.drop >= hints[worst].drop {
				worst = i
			}
		}
		hints = slices.Delete(hints, worst, worst+1)
	}
}

// footer is the menu hints, the last error, or the service count and keys.
// It is clipped to the terminal: a footer that wraps pushes the whole view
// down a line.
func (m *statusModel) footer() string {
	var text string
	switch {
	case m.commandPrompt:
		text = " $ " + m.commandText + "▏" +
			dimStyle.Render("  enter run · esc cancel")
	case m.menu != nil:
		text = dimStyle.Render(" j/k select · enter run · esc cancel")
	case m.errText != "":
		text = redStyle.Render(" " + strings.ReplaceAll(m.errText, "\n", " · "))
	default:
		var b strings.Builder
		b.WriteString(fmt.Sprintf(" %d services", len(m.services)))
		if m.statsErr != "" {
			b.WriteString(redStyle.Render("  ·  stats: " + m.statsErr))
		}
		// The hints get whatever the count and the stats error leave.
		budget := 0
		if m.width > 0 {
			budget = m.width - lipgloss.Width(b.String()) - len("  ·  ")
		}
		b.WriteString(dimStyle.Render("  ·  " + m.footerHints(budget)))
		text = b.String()
	}
	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(text)
	}
	return text
}

// renderHostLine is the machine's resource summary, empty until the first
// sample arrives (or forever, when no host fetch is configured). Each part
// is dropped individually when the host did not report it, so a kernel
// without one of these still yields a useful line.
func (m *statusModel) renderHostLine() string {
	if !m.metricsLoaded {
		return ""
	}
	metrics := m.metrics

	var parts []string
	if metrics.HasLoad() {
		parts = append(parts,
			loadStyle(metrics.LoadPerCPU()).Render(fmt.Sprintf("load %.2f", metrics.Load1))+
				dimStyle.Render(fmt.Sprintf(" %.2f/cpu", metrics.LoadPerCPU())))
	}
	if metrics.MemTotalKB > 0 {
		percent := metrics.MemUsedPercent()
		parts = append(parts, fmt.Sprintf("mem %s/%s %s",
			formatKB(metrics.MemUsedKB()), formatKB(metrics.MemTotalKB),
			usageStyle(percent).Render(fmt.Sprintf("%.0f%%", percent))))
	}
	if metrics.DiskTotalKB > 0 {
		percent := metrics.DiskUsedPercent()
		parts = append(parts, fmt.Sprintf("disk %s/%s %s",
			formatKB(metrics.DiskUsedKB), formatKB(metrics.DiskTotalKB),
			usageStyle(percent).Render(fmt.Sprintf("%.0f%%", percent))))
	}
	if metrics.Uptime > 0 {
		parts = append(parts, dimStyle.Render("up "+formatUptime(metrics.Uptime)))
	}
	if len(parts) == 0 {
		return ""
	}
	line := " " + strings.Join(parts, "  ")
	if m.metricsStale {
		line += dimStyle.Render("  (stale)")
	}
	return line
}

// usageStyle colors a percentage of a finite resource.
func usageStyle(percent float64) lipgloss.Style {
	switch {
	case percent >= 90:
		return redStyle
	case percent >= 75:
		return yellowStyle
	default:
		return greenStyle
	}
}

// loadStyle colors load normalized per core, where 1.0 means every core is
// busy — the only reading that is comparable across machines.
func loadStyle(perCPU float64) lipgloss.Style {
	switch {
	case perCPU >= 1:
		return redStyle
	case perCPU >= 0.7:
		return yellowStyle
	default:
		return greenStyle
	}
}

// formatKB renders a kibibyte count compactly: 3 significant digits at
// most, so the header keeps a stable width as values move.
func formatKB(kb uint64) string {
	units := []string{"K", "M", "G", "T", "P"}
	value, unit := float64(kb), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%s", value, units[unit])
	}
	return fmt.Sprintf("%.1f%s", value, units[unit])
}

// formatUptime renders a duration at the coarsest useful resolution.
func formatUptime(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// renderMenu shows the open menu's entries, each with the command it runs,
// centered where the table normally is.
func (m *statusModel) renderMenu(width, height int) string {
	labelWidth := 0
	for _, entry := range m.menu.entries {
		labelWidth = max(labelWidth, len(entry.label))
	}
	var lines []string
	for i, entry := range m.menu.entries {
		line := fmt.Sprintf(" %-*s  ", labelWidth, entry.label)
		if i == m.menu.selected {
			lines = append(lines, reverseStyle.Render(line+entry.command+" "))
		} else {
			lines = append(lines, boldStyle.Render(line)+dimStyle.Render(entry.command+" "))
		}
	}
	menu := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	if width > 0 && height > 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, menu)
	}
	return menu
}

// cell is one table cell: its text, and the style applied when the row is
// not the selected one.
type cell struct {
	text  string
	style lipgloss.Style
}

// tableHeaders are the column titles for the current state. CPU and MEM
// exist whenever something can fill them, RESTARTS whenever the inspect
// behind it answered.
func (m *statusModel) tableHeaders() []string {
	headers := []string{"SERVICE", "STATE", "HEALTH"}
	if m.restartColumn() {
		headers = append(headers, "RESTARTS")
	}
	if m.statsColumns() {
		headers = append(headers, "CPU", "MEM")
	}
	return append(headers, "PORTS", "STATUS")
}

// restartColumn reports whether the table carries RESTARTS. The counts come
// from a single `docker inspect` over the whole list, so in practice they
// are known for every service or for none — the column does not flicker
// with individual rows.
func (m *statusModel) restartColumn() bool {
	for i := range m.services {
		if m.services[i].Restarts != nil {
			return true
		}
	}
	return false
}

// serviceRow builds one service's cells, aligned with tableHeaders.
func (m *statusModel) serviceRow(s compose.Service) []cell {
	health := s.Health
	if health == "" {
		health = "-"
	}
	row := []cell{
		{text: s.Service},
		{text: s.State, style: stateStyle(s.State)},
		{text: health, style: healthStyle(s.Health)},
	}
	if m.restartColumn() {
		restarts := cell{text: s.RestartsText(), style: dimStyle}
		if s.Restarts != nil {
			restarts.style = restartStyle(*s.Restarts)
		}
		row = append(row, restarts)
	}
	if m.statsColumns() {
		row = append(row, m.statsCells(s.Name)...)
	}
	return append(row, cell{text: s.PortsSummary()}, cell{text: s.Status})
}

// renderTable writes the service table, keeping the selection visible when
// there are more rows than fit.
func (m *statusModel) renderTable(b *strings.Builder, width, height int) {
	headers := m.tableHeaders()
	rows := make([][]cell, len(m.services))
	for i, s := range m.services {
		rows[i] = m.serviceRow(s)
	}
	widths := columnWidths(headers, rows, width)

	pad := func(s string, w int) string {
		if len(s) > w {
			if w <= 1 {
				return strings.Repeat(".", max(w, 0))
			}
			return s[:w-1] + "…"
		}
		return s + strings.Repeat(" ", w-len(s))
	}
	line := func(cells []string) string { return " " + strings.Join(cells, "  ") }

	padded := make([]string, len(headers))
	for i, header := range headers {
		padded[i] = pad(header, widths[i])
	}
	b.WriteString(dimStyle.Render(line(padded)))
	b.WriteString("\n")

	visible := max(height-1, 1)
	offset := 0
	if m.selected >= visible {
		offset = m.selected - visible + 1
	}
	for i := offset; i < min(len(rows), offset+visible); i++ {
		texts := make([]string, len(rows[i]))
		for j, c := range rows[i] {
			texts[j] = pad(c.text, widths[j])
		}
		if i == m.selected {
			// One uniform style for the selected row keeps the highlight
			// readable over the per-cell colors.
			b.WriteString(reverseStyle.Render(line(texts)))
		} else {
			for j, c := range rows[i] {
				texts[j] = c.style.Render(texts[j])
			}
			b.WriteString(line(texts))
		}
		b.WriteString("\n")
	}
}

// columnWidths sizes every column to its widest content, then gives the
// last one (STATUS) whatever width is left.
func columnWidths(headers []string, rows [][]cell, width int) []int {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, c := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], len(c.text))
			}
		}
	}
	if width <= 0 {
		return widths
	}
	last := len(widths) - 1
	used := 1 // the leading space
	for i := range last {
		used += widths[i] + 2
	}
	widths[last] = max(len(headers[last]), width-used-1)
	return widths
}

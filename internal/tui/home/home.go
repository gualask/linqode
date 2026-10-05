// Package home is the screen the session opens on: a header naming the
// target and the machine's vital signs, a body of focusable panels, and a
// footer saying what the keys do.
//
// It owns the screen, which the status view used to: the title line, the
// footer, the modal menus, the `!` prompt, and which region the keys are
// talking to. A feature package owns what goes inside its own panel and
// nothing beyond it, so that adding a panel is adding a panel rather than
// editing a screen.
//
// The gestures are two. `tab` moves focus between panels; `enter` descends
// one level on whichever has it — into a service's logs from the table, into
// the system view from the host band, into the logs of the container an
// event happened to from the feed. Nothing here maximises a panel: opening a
// detail changes context, where a layout gesture would only change layout.
//
// The table is the anchor and fills the body; anything else is a satellite,
// which is the thing that gives up its rows when the terminal runs short.
package home

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/tui/events"
	"github.com/gualask/linqode/internal/tui/panel"
	"github.com/gualask/linqode/internal/tui/status"
	"github.com/gualask/linqode/internal/tui/system"
)

// The intervals the screen asks for. What it actually gets is stretched by
// the sampler when a link is slow enough to earn it.
const (
	// servicesRefresh is the cadence of `compose ps` when nothing is telling
	// the screen what changed: ~60 ms of server time per read
	// (docs/PROJECT.md, dashboard cost budget).
	servicesRefresh = 5 * time.Second
	// servicesWatched is that cadence once the daemon's event stream is up.
	// It is a safety net, not the mechanism: it catches what no event
	// describes and a stream that quietly stopped delivering, at a twelfth
	// of the round-trips.
	servicesWatched = 60 * time.Second
	// hostRefresh is the meters' cadence. At ~2 ms it is noise beside the
	// `ps` on the same clock.
	hostRefresh = 5 * time.Second
	// statsRefresh is the container readings' cadence. It used to be twenty
	// seconds because `docker stats` costs ~2 s of server time; reading the
	// cgroups directly costs what any other /proc read costs, so the limit
	// is now what an operator can use rather than what the server can bear.
	// Five seconds also means the first CPU percentage — which needs two
	// readings to exist at all — arrives while they are still looking.
	statsRefresh = 5 * time.Second
	// processesRefresh is the process table's cadence *while the system view
	// is open*, and never otherwise. One reading is about 220 bytes per
	// process — 20 KB on an ordinary host — which is far too much to pay for
	// a screen nobody is looking at, and reasonable to pay for one an
	// operator is looking at on purpose. Three seconds rather than five
	// because this is the tier that is asked for: the second reading is the
	// first with a CPU share on it.
	processesRefresh = 3 * time.Second
	// diskUsageRefresh is `docker system df`, read while the services panel
	// has room to draw it and not otherwise. It is the one reading here that
	// is genuinely slow on a real host — the daemon walks the image store,
	// the volumes and the build cache to answer — and it is also the one that
	// changes least, so once a minute is often enough. The sampler's stretch
	// rule is the guard for a host where it takes seconds: a read that fills
	// a quarter of this interval widens it.
	diskUsageRefresh = time.Minute
	// gpuRefresh is the graphics cards', on the same gate as the process
	// table. Five seconds rather than three because one of the two vendors
	// answers only through a tool that initialises a driver context, and
	// what that costs is measured in hundreds of milliseconds rather than
	// in the milliseconds every other reading here costs.
	gpuRefresh = 5 * time.Second
)

// The samples, delivered asynchronously so the UI never blocks on an SSH
// round-trip. Each carries the reading its source asked for.
type (
	servicesSampleMsg struct {
		services []compose.Service
		err      error
	}
	hostSampleMsg struct {
		metrics host.Metrics
		err     error
	}
	statsSampleMsg struct {
		sample compose.CgroupSample
		err    error
	}
	processSampleMsg struct {
		sample host.ProcessSample
		err    error
	}
	diskUsageMsg struct {
		usage []compose.DiskUsage
		err   error
	}
	gpuSampleMsg struct {
		gpus []host.GPU
		err  error
	}
)

// Config is the session's static context, plus the previews the confirmation
// menu shows before running anything.
type Config struct {
	// Target is the `user@host` the session is connected to.
	Target string
	// ComposeDir is the remote project directory shown in the header.
	ComposeDir string
	// DockerEndpoint is the daemon this session reaches when it is not the
	// host's own socket. Empty is the ordinary case and shows nothing.
	DockerEndpoint string
	// OS is what the host calls itself, from the connect-time probe. Empty on
	// a host that did not say, and then the system view simply has one row
	// fewer.
	OS string
	// ComposeUnavailable is why this host has no compose, from the same
	// probe, and empty when it has one. Services is nil whenever this is set;
	// this is the sentence that says why, which nil alone cannot.
	ComposeUnavailable string
	// Scripts are the predefined commands from the config, sorted by name.
	Scripts []operations.Script
	// ActionPreview supplies the exact operations-owned command that the
	// human confirmation menu shows before running it.
	ActionPreview func(operations.ServiceAction, string) string

	// The readings the screen samples. Each blocks on an SSH round-trip and
	// is always called from a background command, never from the UI loop.
	// A nil one is a capability this host does not offer: no fetch, no
	// cadence, and nothing on screen that would sit empty waiting for it.
	Services func() ([]compose.Service, error)
	Host     func() (host.Metrics, error)
	// Stats reads the container counters for the services it is given. The
	// screen keeps the previous reading, because a percentage is the
	// difference between two of them.
	Stats func(services []compose.Service) (compose.CgroupSample, error)
	// Processes reads the machine's process table. It is the first reading
	// that is never taken for the home: the sampler's gate keeps it to the
	// system view, and opening that view is what asks for it.
	Processes func() (host.ProcessSample, error)
	// DiskUsage asks the daemon what it is holding — images, containers,
	// volumes, build cache. Read while the services panel draws it, on a
	// much slower clock than anything else: it is the one
	// reading that is genuinely slow on a real host.
	DiskUsage func() ([]compose.DiskUsage, error)
	// GPUs reads the graphics cards. Same gate again, and for the same
	// reason the process table is on it: one vendor answers only through a
	// tool that costs hundreds of milliseconds to start.
	GPUs func() ([]host.GPU, error)

	// Watch streams the daemon's changes to this project's containers. Nil
	// leaves the service list on its timer, which is what it falls back to
	// if the stream fails or ends.
	Watch func(project string) (operations.Feed, error)
}

type Model struct {
	info Config

	// services is the compose table, the anchor panel. It is held by its
	// concrete type as well as through the panel ring because the screen
	// asks it questions no panel interface should carry — which service is
	// selected, what the host sample says.
	services *status.Model
	// system is the machine itself: the header band, and the view an `enter`
	// on it opens.
	system *system.Model
	// events is the feed of what the daemon reported, nil when this session
	// has no stream to fill it.
	events *events.Model

	panels []panel.Panel
	// eventsIndex is where the feed sits in the ring, -1 when there is none.
	// The panel is in the ring but not always on screen: a short terminal
	// gives its rows back to the table, and focus has to skip what is not
	// drawn.
	eventsIndex int
	// anchor is the panel that fills the body; focus is the one the keys are
	// talking to, which is not the same thing — the band takes focus without
	// ever leaving the header.
	anchor int
	focus  int
	// detail is the view an `enter` opened over the body, nil on the home.
	detail panel.Panel

	// sampler owns what is read off the host and how often.
	sampler *sampler
	// project is what `ps` reported this session's containers belong to, and
	// what scopes the daemon's event stream.
	project string
	// watch is the daemon's event stream while it is up, nil otherwise.
	watch         *operations.Feed
	watchStarting bool
	// watchOpened is when the stream now up was opened. watchRetry is how
	// long the last one that ended quickly, or was refused, put the next
	// attempt off, and watchRetryAt is when that wait is over.
	watchOpened  time.Time
	watchRetry   time.Duration
	watchRetryAt time.Time
	// containers is the service list the last reading described, kept
	// because the counters are addressed by container id and by process.
	containers []compose.Service
	// previousCgroups is the reading the next one is measured against.
	previousCgroups compose.CgroupSample

	// servicesStale records that something changed while a read was already
	// in flight: that read answers a question older than the news, so
	// another one follows it.
	servicesStale bool

	// menu is the open modal list, nil when none is. While one is open every
	// key routes to it instead of to the focused panel.
	menu *menu

	// commandPrompt is the `!` ad-hoc command line. While it is open every
	// key edits the text, so `q` types a q instead of quitting. commandText
	// is what has been typed; lastCommand is what was last run, which the
	// prompt reopens with — the same courtesy `f` does for log filters.
	commandPrompt bool
	commandText   string
	lastCommand   string

	// pending is the feed the application is opening, said in the footer
	// until it opens or is given up on; empty when nothing is.
	pending string

	width, height int
}

func New(config Config, services *status.Model) *Model {
	m := &Model{info: config, services: services,
		system: system.New(config.OS, config.ComposeUnavailable)}
	m.sampler = newSampler(map[sourceID]*source{
		sourceServices: {every: servicesRefresh, start: read(config.Services,
			func(services []compose.Service, err error) tea.Msg {
				return servicesSampleMsg{services: services, err: err}
			})},
		sourceHost: {every: hostRefresh, start: read(config.Host,
			func(metrics host.Metrics, err error) tea.Msg {
				return hostSampleMsg{metrics: metrics, err: err}
			})},
		// The live stream feeds the same columns a second at a time, so
		// while it runs the screen stops paying two seconds for a staler
		// answer.
		sourceStats: {every: statsRefresh, gate: func() bool { return !services.LiveActive() },
			start: m.readCgroups},
		// The on-demand tier: read while the view that shows it is open, and
		// not otherwise. This is what phase A's panel model was for — a
		// panel nobody is looking at costs nothing.
		sourceProcesses: {every: processesRefresh,
			gate: m.systemShown,
			start: read(config.Processes,
				func(sample host.ProcessSample, err error) tea.Msg {
					return processSampleMsg{sample: sample, err: err}
				})},
		// What docker holds on disk is about docker, not about the machine, so
		// it is drawn under the table rather than among the machine's
		// readings — and it is read while that section is drawn.
		sourceDiskUsage: {every: diskUsageRefresh,
			gate: m.diskUsageShown,
			start: read(config.DiskUsage,
				func(usage []compose.DiskUsage, err error) tea.Msg {
					return diskUsageMsg{usage: usage, err: err}
				})},
		sourceGPU: {every: gpuRefresh,
			gate: m.systemShown,
			start: read(config.GPUs,
				func(gpus []host.GPU, err error) tea.Msg {
					return gpuSampleMsg{gpus: gpus, err: err}
				})},
	})
	// Top to bottom, the way `tab` walks them, and focus starts at the top.
	//
	// The machine is often the reason the session was opened at all, and it
	// was two keystrokes away — `shift+tab` backwards past the feed, then
	// `enter` — while the table it starts on is what fills the body anyway
	// and can be reached with one `tab` forward. The cost is real and worth
	// naming: `j`, `k` and `enter` do nothing until that `tab`, because the
	// header answers none of them. It buys the machine one keystroke and
	// makes the ring walk forward from where it starts.
	m.panels = []panel.Panel{m.system, services}
	m.anchor, m.focus = 1, 0
	if config.Host == nil {
		// No meters on this host, so no header box and nothing at the top to
		// focus: the table is the only thing drawn.
		m.focus = 1
	}
	// A host with no compose has no table to anchor the screen on, and giving
	// the body to a panel that can only explain its own absence would be
	// showing the absence at the largest size on the screen. The machine
	// takes the body instead: the readings are all still there, and this
	// becomes what it can honestly be, which is a machine monitor. The
	// sentence about compose goes with them, as the view's first row.
	//
	// Only when there is a machine to show, though. With host_metrics off as
	// well there is nothing behind the band either, and then the panel that
	// at least says why is the better one to be looking at.
	if config.Services == nil && config.Host != nil {
		m.panels = []panel.Panel{m.system}
		m.anchor, m.focus = 0, 0
		// It is the view rather than the band from the start, and stays that
		// way: there is no `enter` to press and nothing behind it.
		m.system.SetOpen(true)
	}
	m.eventsIndex = -1
	if config.Watch != nil {
		m.events = events.New()
		m.panels = append(m.panels, m.events)
		m.eventsIndex = len(m.panels) - 1
	}
	m.applyFocus()
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.sampler.due(), heartbeat())
}

// read turns a fetch into the command that carries its outcome back. A nil
// fetch yields a nil start, which is how the sampler knows a reading is not
// available on this host.
func read[T any](fetch func() (T, error), wrap func(T, error) tea.Msg) func() tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Cmd {
		return func() tea.Msg { return wrap(fetch()) }
	}
}

// systemShown reports whether the machine's readings are on screen, which is
// what the on-demand tier is gated on. There are two ways for them to be
// there — opened over the home with `enter`, or holding the body because this
// host has no table — and a reading nobody is looking at must be taken in
// neither.
func (m *Model) systemShown() bool {
	return m.detail == panel.Panel(m.system) ||
		(m.detail == nil && m.panels[m.anchor] == panel.Panel(m.system))
}

// diskUsageShown reports whether the services panel is on screen with room for
// what docker holds on disk. The section only takes rows the table leaves
// empty, so a project long enough to fill the panel pays nothing for it.
//
// A terminal too short to give the panel a row inside its border has no room
// for it either, and is asked here rather than of the panel: the panel is not
// told a size of zero, which it would take for a size not known yet.
func (m *Model) diskUsageShown() bool {
	if frame := m.frame(); frame.known && frame.body.content().height == 0 {
		return false
	}
	return m.detail == nil && m.panels[m.anchor] == panel.Panel(m.services) &&
		m.services.DiskUsageRoom()
}

// frame is the current layout, computed from the same inputs the rendering
// uses so the two can never disagree about what is on screen.
func (m *Model) frame() frame {
	// The header is a box wherever this host reports meters at all, sample or
	// not: one that appeared on the first reading would push the body down a
	// line a second after the screen opened, and would make the first stop of
	// the focus ring exist only after a round trip.
	return layoutFor(m.width, m.height, m.info.Host != nil, m.events != nil)
}

// SetSize records the terminal. The panels are sized at render time instead,
// from the same layout the rendering uses, so the two can never disagree about
// how much room a panel was given.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	// A terminal that shrank far enough took the satellite off the screen
	// with it; focus cannot stay on a panel that is no longer drawn.
	if !m.onScreen(m.focus) {
		m.moveFocus(-1)
	}
}

// SetPending says what the application is opening on the screen's behalf, or
// nothing with "". Opening is a round trip and every other request waits for
// it, so the footer says so, and says how to give up on it.
func (m *Model) SetPending(text string) { m.pending = text }

// Modal reports whether a menu or the `!` prompt is open, which take every
// key — esc included — while they are.
func (m *Model) Modal() bool { return m.menu != nil || m.commandPrompt }

// SetError shows a failure the screen itself learned about — starting a feed,
// so far — on the panel whose data it concerns.
func (m *Model) SetError(text string) { m.services.SetError(text) }

// Refresh reads everything again, now: what `r` means, and what the
// application asks for when a feed closes so an action's effect is visible
// immediately. The container readings are left out — two seconds of server
// time to confirm what the table already shows is not what anyone means by
// refresh.
func (m *Model) Refresh() tea.Cmd {
	return tea.Batch(m.sampler.read(sourceServices), m.sampler.read(sourceHost))
}

// UpdateBackground keeps the panels' timers and asynchronous results alive
// while another view is on screen, without starting fetches nobody can see.
func (m *Model) UpdateBackground(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case beatMsg:
		// The heartbeat stays alive so the cadence resumes on return, but
		// nothing is read for a screen nobody is looking at.
		return heartbeat(), true
	case servicesSampleMsg, hostSampleMsg, statsSampleMsg, processSampleMsg,
		diskUsageMsg, gpuSampleMsg:
		// A read already in flight when the view opened still lands.
		return m.applySample(msg), true
	case watchTickMsg:
		// The stream keeps draining: a log view is exactly when a container
		// is most likely to change, and its channel must not fill up.
		return m.handleWatchTick(), true
	case watchFeedMsg:
		return m.applyWatchFeed(msg), true
	}
	return m.services.UpdateBackground(msg)
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case beatMsg:
		return tea.Batch(m.sampler.due(), heartbeat())
	case servicesSampleMsg, hostSampleMsg, statsSampleMsg, processSampleMsg,
		diskUsageMsg, gpuSampleMsg:
		return m.applySample(msg)
	case watchFeedMsg:
		return m.applyWatchFeed(msg)
	case watchTickMsg:
		return m.handleWatchTick()
	}
	// Everything else belongs to the table: its live stream, and the ticks
	// that drain it.
	return m.services.Update(msg)
}

// applySample hands one reading to whichever panel shows it, and tells the
// sampler its source is free again.
func (m *Model) applySample(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case servicesSampleMsg:
		m.services.SetServices(msg.services, msg.err)
		m.sampler.finished(sourceServices)
		// Only a read that answered is a reason to watch: one that failed
		// says the daemon is not there to be watched, and the project it
		// would scope the stream to is the last good read's anyway.
		var watch tea.Cmd
		if msg.err == nil {
			m.containers = msg.services
			if m.events != nil {
				// The feed names containers the way the table does, and
				// opens the right logs, from the same list.
				m.events.SetServices(msg.services)
			}
			if len(msg.services) > 0 {
				m.project = msg.services[0].Project
			}
			watch = m.startWatching()
		}
		if m.servicesStale {
			// News arrived while this read was in flight, so it answers a
			// question that is already out of date.
			m.servicesStale = false
			return tea.Batch(watch, m.sampler.read(sourceServices))
		}
		return watch
	case hostSampleMsg:
		m.system.SetSample(msg.metrics, msg.err)
		m.sampler.finished(sourceHost)
	case statsSampleMsg:
		m.applyCgroups(msg)
		m.sampler.finished(sourceStats)
	case processSampleMsg:
		m.system.SetProcesses(msg.sample, msg.err)
		m.sampler.finished(sourceProcesses)
	case diskUsageMsg:
		m.services.SetDiskUsage(msg.usage, msg.err)
		m.sampler.finished(sourceDiskUsage)
	case gpuSampleMsg:
		m.system.SetGPUs(msg.gpus, msg.err)
		m.sampler.finished(sourceGPU)
	}
	return nil
}

// readCgroups samples the container counters for the services the last
// reading described. Nothing is read before there is a service list: the
// counters are addressed by container id and by process, and both come from
// `ps` and the inspect that follows it.
func (m *Model) readCgroups() tea.Cmd {
	if m.info.Stats == nil || len(m.containers) == 0 {
		return nil
	}
	fetch, containers := m.info.Stats, m.containers
	return func() tea.Msg {
		sample, err := fetch(containers)
		return statsSampleMsg{sample: sample, err: err}
	}
}

// applyCgroups turns two readings into what the table shows. The first one
// after connecting has nothing to be measured against, so it fills in
// everything except the percentages and leaves those for the next.
func (m *Model) applyCgroups(msg statsSampleMsg) {
	if msg.err != nil {
		m.services.SetStats(nil, msg.err)
		return
	}
	// A container with no limit of its own is bounded by the machine, which
	// is the number docker shows too — and which the meters already know.
	hostMemBytes := m.system.Metrics().MemTotalKB * 1024
	m.services.SetStats(
		m.previousCgroups.Delta(msg.sample, m.containers, hostMemBytes), nil)
	m.previousCgroups = msg.sample
}

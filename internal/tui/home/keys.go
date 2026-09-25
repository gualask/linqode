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
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gualask/linqode/internal/tui/panel"
)

// What the keys do: which panel has them, how focus moves between panels,
// and what a key opens from where it is pressed.

// focused is the panel the keys are talking to. The ring is never empty, so
// this never returns nil.
func (m *Model) focused() panel.Panel { return m.panels[m.focus] }

// applyFocus tells every panel whether it currently holds focus, so a
// selection outside the focused panel can recede instead of competing.
func (m *Model) applyFocus() {
	for index, p := range m.panels {
		p.SetFocus(index == m.focus)
	}
}

func (m *Model) moveFocus(delta int) {
	for range len(m.panels) {
		m.focus = (m.focus + delta + len(m.panels)) % len(m.panels)
		if m.onScreen(m.focus) {
			break
		}
	}
	m.applyFocus()
}

// onScreen reports whether a panel is currently drawn. Focus must never land
// on one that is not: the ring would move without anything changing, and the
// footer would offer the keys of a region nobody can see.
//
// Two of the three can fail to be drawn. The satellite gives up its rows on a
// short terminal. The machine is drawn as the header's box, or as the body on
// a host with no compose, or — before the first sample lands, and on a host
// where `host_metrics` is off — not at all.
func (m *Model) onScreen(index int) bool {
	switch {
	case index == m.eventsIndex:
		return m.frame().events.height > 0
	case m.panels[index] == panel.Panel(m.system):
		return m.frame().headerBox || m.anchor == index
	default:
		return true
	}
}

// drawnPanels is how many of the ring an operator can actually reach. It is
// what decides whether the ring is worth advertising: on a screen where only
// one panel is drawn, `tab` moves nothing.
func (m *Model) drawnPanels() int {
	drawn := 0
	for index := range m.panels {
		if m.onScreen(index) {
			drawn++
		}
	}
	return drawn
}

// headerFocused reports whether the header's box wears the focus accent.
//
// The machine can be on screen twice — the band in the header, the readings
// in the body, on a host with no compose to put there — and only one of them
// may be lit, or the screen says two regions have focus when the whole point
// of the accent is that exactly one does. The body wins: it is where the keys
// go. A detail takes the accent for the same reason.
func (m *Model) headerFocused() bool {
	return m.detail == nil &&
		m.focused() == panel.Panel(m.system) &&
		m.panels[m.anchor] != panel.Panel(m.system)
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.commandPrompt {
		return m.handleCommandKey(msg)
	}
	if m.menu != nil {
		return m.handleMenuKey(msg)
	}
	// `c` is the one command on this screen whose object is a selection, and
	// a selection belongs to a panel. So it is claimed here only where the
	// region with focus has one; anywhere else the key is left alone and
	// travels on to that region with everything else the screen does not
	// claim. It used to sit in the switch below, beside `r` and `x`, and read
	// the table's selection whoever had focus — a lifecycle menu about a
	// service chosen somewhere else, and, from the system view, about a
	// service that was not on the screen at all.
	if msg.String() == "c" {
		if service, offered := m.actionsHere(); offered && service != "" {
			m.openActionMenu(service)
			return nil
		}
	}

	switch msg.String() {
	case "esc":
		// Back, and only back. On the home there is nothing above to come
		// back to, so it does nothing at all — leaving the application is
		// `q`, and a key that means "up one level" everywhere else must not
		// also mean "throw this session away" at the top, where the two are
		// one keystroke apart and only one of them is undoable.
		if m.detail != nil {
			m.closeDetail()
		}
		return nil
	case "q":
		// The way out, from wherever you are. It is `esc` that walks back up
		// a level at a time; a second key doing the same thing left the
		// footer's `q quit` a lie in every view that had one open.
		return tea.Quit
	case "ctrl+c":
		return tea.Quit
	case "tab", "shift+tab":
		// A detail has taken the body, and the ring with it: the panels these
		// keys move between are not on screen. Moving focus around an
		// invisible ring changes nothing an operator can see and everything
		// they find when they press esc — they opened the system view from
		// the header and came back to the table — which is the shape of a
		// surprise rather than of a feature.
		if m.detail != nil {
			return nil
		}
		if msg.String() == "tab" {
			m.moveFocus(1)
		} else {
			m.moveFocus(-1)
		}
	case "enter":
		// `enter` descends one level, and a detail is the level below: there
		// is nothing under it to open, and the panel it would have descended
		// from is not the one being shown. It goes to the detail instead,
		// with every other key the screen does not claim.
		if m.detail != nil {
			return m.detail.Update(msg)
		}
		return m.open()
	case "r":
		// Refresh is the screen's, not a panel's: what an operator means by
		// it is "read everything again, now".
		return m.Refresh()
	case "x":
		m.openScriptMenu()
	case "!":
		m.commandPrompt, m.commandText = true, m.lastCommand
		m.services.SetError("")
	default:
		// Anything the screen does not claim belongs to the panel with
		// focus — or to the detail, when one has taken the screen from it.
		if m.detail != nil {
			return m.detail.Update(msg)
		}
		return m.focused().Update(msg)
	}
	return nil
}

// open descends one level on the focused panel: from the table into the
// selected service's logs, from the band into the system view, from an event
// into the logs of the container it happened to. One gesture, three
// destinations, and each of them is the obvious next question about what has
// focus.
//
// Two of the three are the same destination reached from two panels, so they
// are one branch: whatever has a service under the cursor opens its logs, and
// which panels those are is theirs to say (panel.ServiceRegion). The machine
// is the one that descends into something other than a service, and it is
// matched on identity because it is the panel this screen holds the detail
// slot for.
func (m *Model) open() tea.Cmd {
	// On identity rather than on index: which panel sits where is no longer
	// fixed, and a screen whose body is the machine would otherwise read the
	// anchor as the table.
	if m.focused() == panel.Panel(m.system) {
		// Nothing to descend into when the readings are already the body.
		if m.system.HasBand() && m.anchor != 0 {
			m.detail = m.system
			m.system.SetOpen(true)
			// Opening the view is what asks for the readings it alone
			// shows: they are gated on being here, so waiting for the next
			// beat would be waiting for nothing.
			return tea.Batch(m.sampler.read(sourceProcesses), m.sampler.read(sourceGPU))
		}
		return nil
	}
	// An empty table, or an event about a container that was destroyed, has
	// no logs behind the cursor to open.
	if services, ok := m.focused().(panel.ServiceRegion); ok {
		if service, ok := services.SelectedService(); ok {
			return openLogs(service)
		}
	}
	return nil
}

// actionsHere answers what `c` needs to know: whether the region the keys are
// talking to offers a lifecycle action at all, and which service it would open
// on. Both come from the region itself — see panel.ServiceRegion, which is
// where the two questions and the difference between them are written down.
//
// The screen's part is only deciding which region that is: the panel with
// focus, or the detail that has taken the body from it.
//
// It used to be a switch on panel identity here, with an arm per panel and the
// table's selection read from wherever the key was pressed. That is what put a
// restart menu about a service chosen somewhere else on the band, and about a
// service that was not on screen at all inside the system view.
func (m *Model) actionsHere() (service string, offered bool) {
	region := m.focused()
	if m.detail != nil {
		region = m.detail
	}
	services, ok := region.(panel.ServiceRegion)
	if !ok || !services.OffersServiceKeys() {
		return "", false
	}
	// A region that offers the key does not have to have something under the
	// cursor at this instant, and the two are kept apart deliberately: what a
	// region advertises is its own to decide, and a future panel may want to
	// offer a key while its cursor is between things.
	service, _ = services.SelectedService()
	return service, true
}

// closeDetail returns to the home, and tells the panel it is a header again:
// the keys it answers to and the hints in the footer are not the same in its
// two forms.
func (m *Model) closeDetail() {
	// Unless it is the body, in which case it was never a band to go back to.
	if m.detail == panel.Panel(m.system) && m.panels[m.anchor] != panel.Panel(m.system) {
		m.system.SetOpen(false)
	}
	m.detail = nil
}

func openLogs(service string) tea.Cmd {
	return openRequest(OpenLogsMsg{Title: "logs: " + service, Service: service})
}

func openRequest(message tea.Msg) tea.Cmd {
	return func() tea.Msg { return message }
}

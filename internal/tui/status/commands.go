package status

// move slides the cursor, clamped to the table. It is the panel's only
// stateful key handling: everything that opens something belongs to the
// screen composing it.
func (m *Model) move(delta int) {
	if len(m.services) == 0 {
		return
	}
	m.selected = min(max(m.selected+delta, 0), len(m.services)-1)
}

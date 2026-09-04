package status

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/tui/theme"
)

type cell struct {
	text  string
	style lipgloss.Style
	// right aligns the cell against the column's right edge. Numbers are
	// read by comparing magnitudes down a column, which only works when
	// their digits line up; the live panel already prints its readings this
	// way, and a table that disagreed with the panel beside it read as two
	// different tools.
	right bool
}

// tableHeaders doubles as the column spec: the header cell's alignment is
// the column's, so a column cannot drift from its own heading.
func (m *Model) tableHeaders() []cell {
	headers := []cell{{text: "SERVICE"}, {text: "STATE"}, {text: "HEALTH"}}
	if m.restartColumn() {
		headers = append(headers, cell{text: "RESTARTS", right: true})
	}
	if m.statsColumns() {
		// Right for the single magnitudes, which is what htop does with
		// CPU%, MEM% and RES: you read them by scanning down the column.
		// Left for the I/O pairs, which is how docker prints them and how a
		// pair wants to be read — "1.45GB/892.3MB" is two numbers, not one
		// magnitude to compare against the row above.
		headers = append(headers, cell{text: "CPU", right: true}, cell{text: "MEM", right: true})
		if m.ioColumns() {
			headers = append(headers, cell{text: "NET RX/TX"}, cell{text: "IO R/W"})
		}
	}
	return append(headers, cell{text: "PORTS"}, cell{text: "STATUS"})
}

// ioColumnsMinWidth is the terminal width below which the network and
// block-device columns are dropped. They cost about 31 columns, and once
// the table has to ellipsize them they stop being readings at all:
// "1.45GB/8…" has lost the half that made it a pair. Measured against a
// four-service project with restart counts, they survive intact from here
// up and truncate below it.
const ioColumnsMinWidth = 120

// ioColumns reports whether there is width to spare for NET RX/TX and
// IO R/W. Unlike CPU and MEM they are cumulative totals, so they are the
// first thing worth giving up when the terminal is the constraint.
func (m *Model) ioColumns() bool {
	return m.statsColumns() && (m.width <= 0 || m.width >= ioColumnsMinWidth)
}

func (m *Model) restartColumn() bool {
	for index := range m.services {
		if m.services[index].Restarts != nil {
			return true
		}
	}
	return false
}

func (m *Model) serviceRow(service compose.Service) []cell {
	health := service.Health
	if health == "" {
		health = "-"
	}
	row := []cell{{text: service.Service}, {text: service.State, style: stateStyle(service.State)},
		{text: health, style: healthStyle(service.Health)}}
	if m.restartColumn() {
		restarts := cell{text: service.RestartsText(), style: theme.Dim, right: true}
		if service.Restarts != nil {
			restarts.style = restartStyle(*service.Restarts)
		}
		row = append(row, restarts)
	}
	if m.statsColumns() {
		row = append(row, m.statsCells(service.Name)...)
	}
	return append(row, cell{text: service.PortsSummary()}, cell{text: service.Status})
}

func (m *Model) renderTable(builder *strings.Builder, width, height int) {
	headers := m.tableHeaders()
	rows := make([][]cell, len(m.services))
	for index, service := range m.services {
		rows[index] = m.serviceRow(service)
	}
	widths, gap := columnLayout(headers, rows, width)
	separator := strings.Repeat(" ", gap)
	pad := func(item cell, cellWidth int) string {
		if len(item.text) > cellWidth {
			if cellWidth <= 1 {
				return strings.Repeat(".", max(cellWidth, 0))
			}
			return item.text[:cellWidth-1] + "…"
		}
		fill := strings.Repeat(" ", cellWidth-len(item.text))
		if item.right {
			return fill + item.text
		}
		return item.text + fill
	}
	line := func(cells []string) string { return " " + strings.Join(cells, separator) }
	// The heading is a band across the whole terminal, not just the cells:
	// padding it out is what stops a table narrower than the screen from
	// looking like it was cut off, and it separates heading from rows
	// without spending a line on a rule.
	padTo := func(text string) string {
		if width <= 0 {
			return text
		}
		return text + strings.Repeat(" ", max(width-lipgloss.Width(text), 0))
	}
	padded := make([]string, len(headers))
	for index, header := range headers {
		padded[index] = pad(header, widths[index])
	}
	builder.WriteString(theme.TableHeader.Render(padTo(line(padded))) + "\n")
	visible := max(height-1, 1)
	offset := 0
	if m.selected >= visible {
		offset = m.selected - visible + 1
	}
	for index := offset; index < min(len(rows), offset+visible); index++ {
		texts := make([]string, len(rows[index]))
		for column, item := range rows[index] {
			// The header owns the alignment, so a row cell that forgot to
			// set it still lines up with its column.
			item.right = headers[column].right
			texts[column] = pad(item, widths[column])
		}
		if index == m.selected {
			builder.WriteString(theme.Reverse.Render(line(texts)))
		} else {
			for column, item := range rows[index] {
				texts[column] = item.style.Render(texts[column])
			}
			builder.WriteString(line(texts))
		}
		builder.WriteString("\n")
	}
}

// The gap between columns, widest first. Two spaces packed the numeric
// columns against each other hard enough to be hard to read across, and
// four is where the eye stops merging neighbours — but four across ten
// columns costs eighteen of them, so a narrow terminal spends that on
// content instead. Pushing the whole slack into the gaps was tried and is
// worse: past about six the row stops reading as one row.
var columnGaps = []int{4, 3, 2}

// columnLayout sizes the columns and picks the gap between them: the widest
// gap whose natural layout still fits, so a wide terminal spends its slack
// on breathing room and a narrow one spends it on content.
func columnLayout(headers []cell, rows [][]cell, width int) ([]int, int) {
	natural := make([]int, len(headers))
	for index, header := range headers {
		natural[index] = len(header.text)
	}
	for _, row := range rows {
		for index, item := range row {
			if index < len(natural) {
				natural[index] = max(natural[index], len(item.text))
			}
		}
	}
	if width <= 0 {
		return natural, columnGaps[0]
	}

	// total is the rendered line: a leading space plus the cells joined by
	// the gap.
	total := func(widths []int, gap int) int {
		sum := 1 + gap*(len(widths)-1)
		for _, columnWidth := range widths {
			sum += columnWidth
		}
		return sum
	}

	gap := columnGaps[len(columnGaps)-1]
	for _, candidate := range columnGaps {
		if total(natural, candidate) <= width {
			gap = candidate
			break
		}
	}

	widths := make([]int, len(natural))
	copy(widths, natural)
	last := len(widths) - 1
	widths[last] = len(headers[last].text)
	// Shrink to fit before handing out slack: giving the last column a
	// floor of its header width used to push the row past the terminal,
	// which wrapped the header onto a second line. Taking from the widest
	// column each time spreads the loss over the ones that can afford it,
	// and `pad` ellipsizes whatever no longer fits.
	for total(widths, gap) > width {
		widest := 0
		for index, columnWidth := range widths {
			if columnWidth > widths[widest] {
				widest = index
			}
		}
		if widths[widest] <= 1 {
			break
		}
		widths[widest]--
	}
	// Whatever is left over goes to the last column, so the selected row's
	// highlight reaches the edge like the heading band does.
	widths[last] += max(width-total(widths, gap), 0)
	return widths, gap
}

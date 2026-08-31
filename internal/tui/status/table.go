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
}

func (m *Model) tableHeaders() []string {
	headers := []string{"SERVICE", "STATE", "HEALTH"}
	if m.restartColumn() {
		headers = append(headers, "RESTARTS")
	}
	if m.statsColumns() {
		headers = append(headers, "CPU", "MEM")
	}
	return append(headers, "PORTS", "STATUS")
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
		restarts := cell{text: service.RestartsText(), style: theme.Dim}
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
	widths := columnWidths(headers, rows, width)
	pad := func(text string, cellWidth int) string {
		if len(text) > cellWidth {
			if cellWidth <= 1 {
				return strings.Repeat(".", max(cellWidth, 0))
			}
			return text[:cellWidth-1] + "…"
		}
		return text + strings.Repeat(" ", cellWidth-len(text))
	}
	line := func(cells []string) string { return " " + strings.Join(cells, "  ") }
	padded := make([]string, len(headers))
	for index, header := range headers {
		padded[index] = pad(header, widths[index])
	}
	builder.WriteString(theme.Dim.Render(line(padded)) + "\n")
	visible := max(height-1, 1)
	offset := 0
	if m.selected >= visible {
		offset = m.selected - visible + 1
	}
	for index := offset; index < min(len(rows), offset+visible); index++ {
		texts := make([]string, len(rows[index]))
		for column, item := range rows[index] {
			texts[column] = pad(item.text, widths[column])
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

func columnWidths(headers []string, rows [][]cell, width int) []int {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = len(header)
	}
	for _, row := range rows {
		for index, item := range row {
			if index < len(widths) {
				widths[index] = max(widths[index], len(item.text))
			}
		}
	}
	if width <= 0 {
		return widths
	}
	last := len(widths) - 1
	used := 1
	for index := range last {
		used += widths[index] + 2
	}
	widths[last] = max(len(headers[last]), width-used-1)
	return widths
}

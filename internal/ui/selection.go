package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// logRow is one on-screen log row; cont marks a wrapped continuation, which
// is rejoined without a newline when copied.
type logRow struct {
	text string
	cont bool
}

type cell struct{ row, col int }

// selection is a character range dragged out in the logs pane.
type selection struct {
	dragging, shown bool
	anchor, cursor  cell
}

// Logs content starts below the header, rule, pane border and details strip.
func (m *Model) logsTop() int { return 2 + 1 + m.detailRows() }
func (m *Model) logsLeft() int {
	if m.narrow() {
		return 2
	}
	return m.leftWidth() + 2
}

func (m *Model) inLogs(x, y int) bool {
	return m.overDetails(x) && x >= m.logsLeft() && x < m.logsLeft()+m.logViewport.Width && y >= m.logsTop() && y < m.logsTop()+m.logViewport.Height
}

// cellAt maps a screen position to a log cell, clamped to the visible rows.
func (m *Model) cellAt(x, y int) cell {
	y = min(max(y, m.logsTop()), m.logsTop()+m.logViewport.Height-1)
	row := min(max(0, m.logViewport.YOffset+y-m.logsTop()), max(0, len(m.logRows)-1))
	return cell{row, min(max(0, x-m.logsLeft()), m.logViewport.Width)}
}

func (m *Model) selectMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if !m.inLogs(msg.X, msg.Y) || len(m.logs) == 0 {
			m.sel = selection{}
			return nil, false
		}
		m.focus = 1
		m.pauseLogs() // freeze rows so the selection stays put
		at := m.cellAt(msg.X, msg.Y)
		m.sel = selection{dragging: true, anchor: at, cursor: at}
		return nil, true
	case msg.Action == tea.MouseActionMotion && m.sel.dragging:
		// Dragging past the pane edge scrolls, so selections can span pages.
		if msg.Y < m.logsTop() {
			m.logViewport.LineUp(1)
		} else if msg.Y >= m.logsTop()+m.logViewport.Height {
			m.logViewport.LineDown(1)
		}
		m.sel.cursor = m.cellAt(msg.X, msg.Y)
		m.sel.shown = m.sel.cursor != m.sel.anchor
		return nil, true
	case msg.Action == tea.MouseActionRelease && m.sel.dragging:
		m.sel.dragging = false
		text := m.selectedText()
		if !m.sel.shown || text == "" {
			m.sel = selection{}
			return nil, true
		}
		return m.copy(text, "Copied "+plural(strings.Count(text, "\n")+1, "line")), true
	}
	return nil, false
}

// span returns the selected columns of row, or ok=false when it is unselected.
func (m *Model) span(row int) (from, to int, ok bool) {
	if !m.sel.shown {
		return 0, 0, false
	}
	a, b := m.sel.anchor, m.sel.cursor
	if b.row < a.row || (b.row == a.row && b.col < a.col) {
		a, b = b, a
	}
	if row < a.row || row > b.row {
		return 0, 0, false
	}
	from, to = 0, m.logViewport.Width
	if row == a.row {
		from = a.col
	}
	if row == b.row {
		to = b.col + 1
	}
	return from, to, true
}

func (m *Model) selectedText() string {
	var b strings.Builder
	first := true
	for i, r := range m.logRows {
		from, to, ok := m.span(i)
		if !ok {
			continue
		}
		if !first && !r.cont {
			b.WriteString("\n")
		}
		first = false
		b.WriteString(ansi.Cut(r.text, from, to))
	}
	return b.String()
}

// visibleLogs renders the logs viewport with the selection highlighted.
func (m *Model) visibleLogs() []string {
	if !m.sel.shown {
		return strings.Split(m.logViewport.View(), "\n")
	}
	var out []string
	for i := 0; i < m.logViewport.Height; i++ {
		row := m.logViewport.YOffset + i
		if row >= len(m.logRows) {
			break
		}
		text := m.logRows[row].text
		from, to, ok := m.span(row)
		if !ok {
			out = append(out, text)
			continue
		}
		mid := ansi.Cut(text, from, to)
		if mid == "" {
			mid = " "
		}
		out = append(out, ansi.Cut(text, 0, from)+selected.Render(mid)+ansi.Cut(text, to, ansi.StringWidth(text)))
	}
	return out
}

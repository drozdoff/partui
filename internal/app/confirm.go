package app

// The delete confirmation: a small menu shown in place of the bottom panel
// and answered with arrows and Enter instead of a single y/n key. While it
// is open (Model.confirm != nil) it receives every key, so the lists cannot
// change between the 'd' press and the answer.

import (
	"fmt"

	"partui/internal/index"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The two answers of the confirmation menu. "Отмена" is the default, so a
// stray Enter closes the dialog without deleting
const (
	confirmDelete = iota
	confirmCancel
)

// confirmDialog asks whether to delete the record of item
type confirmDialog struct {
	item     indexListItem
	selected int
}

// confirmHeight is the rendered height of the dialog: the question, two
// options and the hint line. footerHeight adds the status line on top of it.
const confirmHeight = 4

// deleteSelected opens the delete confirmation for the selected index; like
// verifySelected it requires the index list to have focus, also in the
// expanded mode. Only the database record is deleted — the .par2 files on
// disk are not touched.
func (m *Model) deleteSelected() tea.Cmd {
	if m.focus != FocusIndex {
		return nil
	}
	item, ok := selectedIndexChanged(m)
	if !ok {
		return nil
	}
	// deleting while a verification is running makes no sense: the result
	// would write the record straight back into the database
	if item.status == index.StatusProcessing {
		return nil
	}
	m.confirm = &confirmDialog{item: item, selected: confirmCancel}
	// the dialog is taller than the key hints it replaces: the lists must
	// yield the difference
	m.applyLayout()
	return nil
}

// handleKeyConfirm handles the keys while the confirmation dialog is open and
// swallows everything else: the dialog hides the lists, so no key may reach
// them — not even navigation
func (m *Model) handleKeyConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "down", "left", "right", "k", "j", "tab":
		if m.confirm.selected == confirmDelete {
			m.confirm.selected = confirmCancel
		} else {
			m.confirm.selected = confirmDelete
		}
	case "enter":
		d := m.confirm
		m.confirm = nil
		m.applyLayout()
		if d.selected == confirmDelete {
			return m, m.deleteConfirmed(d.item)
		}
	case "esc":
		m.confirm = nil
		m.applyLayout()
	}
	return m, nil
}

// deleteConfirmed removes the index record from the database and from the
// list. The .par2 files stay on disk, so in the collapsed mode — where the
// list is built from files — the entry reappears on the next rebuild; the
// expanded mode shows the whole database, there the record is gone.
func (m *Model) deleteConfirmed(item indexListItem) tea.Cmd {
	if err := m.app.store.Delete(item.dir, item.par2Filename); err != nil {
		return m.reportError("delete index", err)
	}

	// the expanded list may hold several records with the same file name
	// from different directories, so the entry is found by dir and name
	// together
	for i, it := range m.indexList.Items() {
		entry, ok := it.(indexListItem)
		if ok && entry.dir == item.dir && entry.par2Filename == item.par2Filename {
			m.indexList.RemoveItem(i)
			break
		}
	}

	m.refreshStats()
	return nil
}

// View renders the confirmation menu on the footer context line, in place
// of the key hints, without any background fill (plan UI_revamp, step UI-3)
func (d *confirmDialog) View(width int) string {
	question := styleBold.Render(
		fmt.Sprintf("Удалить запись %s? %s", d.item.par2Filename, d.item.dir))

	options := []struct {
		action int
		label  string
	}{
		{confirmDelete, "Удалить"},
		{confirmCancel, "Отмена"},
	}
	lines := make([]string, 0, len(options)+2)
	lines = append(lines, question)
	for _, opt := range options {
		marker, style := "  ", lipgloss.NewStyle()
		if d.selected == opt.action {
			marker, style = "▸ ", style.Bold(true)
		}
		lines = append(lines, style.Render(marker+opt.label))
	}
	lines = append(lines, styleDim.Render("↑/↓ выбор · Enter · Esc отмена"))

	return lipgloss.NewStyle().
		Padding(0, 1).
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

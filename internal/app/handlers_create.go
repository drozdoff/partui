package app

import (
	"log/slog"
	"partui/internal/index"

	tea "github.com/charmbracelet/bubbletea"
)

// handleParCreateFinished marks the source file as archived, stores the
// index record and refreshes the index list and the bottom panel
func (m *Model) handleParCreateFinished(msg parCreateFinishedMsg) tea.Cmd {
	title := msg.title
	iIdx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
	if iIdx != -1 {
		item.processing = false
		item.archive = true
		m.fileList.SetItem(iIdx, item)
	}

	// Update indexes database. The record is about the .par2 file,
	// not the source file: msg.title is the source file name
	if err := m.app.store.Put(msg.dir, msg.title+".par2", index.StatusOK, m.app.now()); err != nil {
		m.app.logger.Warn("failed to save index to database", slog.String("error", err.Error()))
	}

	// Update indexList - find the entry and mark it as completed
	indexItems := m.indexList.Items()
	iiIndex, ii := findListItem(indexItems, title+".par2", func(ii indexListItem) string { return ii.par2Filename })
	if iiIndex != -1 {
		// status and lastVerified must be updated before the description:
		// the description line renders the status text
		ii.status = index.StatusOK
		ii.lastVerified = m.app.now()
		ii.desc = formatIndexItemDesc(ii, m.indexListExpanded)
		indexItems[iiIndex] = ii
		m.indexList.SetItems(indexItems)
	}

	// Update bottom panel statistics
	m.refreshStats()

	return nil
}

// handleParCreateError releases the source file and marks its index entry
// as damaged
func (m *Model) handleParCreateError(msg parCreateErrorMsg) tea.Cmd {
	clearCmd := m.reportError("PAR2 creation failed", msg.err)

	title := msg.title
	idx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
	if idx != -1 {
		item.processing = false
		m.fileList.SetItem(idx, item)
	}

	// Update indexList - mark entry as damaged
	indexItems := m.indexList.Items()
	iiIndex, ii := findListItem(indexItems, title+".par2", func(ii indexListItem) string { return ii.par2Filename })
	if iiIndex != -1 {
		ii.status = index.StatusDamaged
		ii.lastVerified = m.app.now()
		ii.desc = formatIndexItemDesc(ii, m.indexListExpanded)
		indexItems[iiIndex] = ii
		m.indexList.SetItems(indexItems)
	}

	// Update bottom panel statistics
	m.refreshStats()

	return clearCmd
}

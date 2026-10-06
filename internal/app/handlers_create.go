package app

import (
	"partui/internal/index"

	tea "github.com/charmbracelet/bubbletea"
)

// handleParCreateFinished marks the source file as archived, stores the
// index record and refreshes the index list and the bottom panel
func (m *Model) handleParCreateFinished(msg parCreateFinishedMsg) tea.Cmd {
	// Update indexes database. The record is about the .par2 file,
	// not the source file: msg.title is the source file name.
	// Ignored-with-log level: the archive exists, only the persistent
	// status is lost and will be restored on the next verification
	if err := m.app.store.Put(msg.dir, msg.title+".par2", index.StatusOK, m.app.now()); err != nil {
		m.app.logStoreError("failed to save index to database", err)
	}

	// mark the source file as archived
	m.updateFileItem(msg.title, func(fi *fileListItem) {
		fi.processing = false
		fi.archive = true
	})

	// mark the index entry as completed
	m.updateIndexItem(msg.title+".par2", func(ii *indexListItem) {
		ii.status = index.StatusOK
		ii.lastVerified = m.app.now()
	})

	// Update bottom panel statistics
	m.refreshStats()

	return nil
}

// handleParCreateError releases the source file and marks its index entry
// as damaged
func (m *Model) handleParCreateError(msg parCreateErrorMsg) tea.Cmd {
	clearCmd := m.reportError("PAR2 creation failed", msg.err)

	m.updateFileItem(msg.title, func(fi *fileListItem) {
		fi.processing = false
	})

	m.updateIndexItem(msg.title+".par2", func(ii *indexListItem) {
		ii.status = index.StatusDamaged
		ii.lastVerified = m.app.now()
	})

	// Update bottom panel statistics
	m.refreshStats()

	return clearCmd
}

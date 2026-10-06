package app

import (
	"log/slog"
	"partui/internal/index"

	tea "github.com/charmbracelet/bubbletea"
)

// handleVerifyFinished persists the verification result, updates the index
// entry and the bottom panel. A missing directory (StatusMissing) keeps the
// old LastVerified — the index was not actually verified.
func (m *Model) handleVerifyFinished(msg indexVerifyFinishedMsg) tea.Cmd {
	clearCmd := m.reportError("PAR2 verification failed", msg.err)

	title := msg.par2Filename

	// never persist records under names that cannot be a PAR2 file
	if !isPar2Name(title) {
		// defensive check: startup cleanup removes such records, so this
		// branch is next to unreachable; a log entry is enough
		m.app.logger.Warn("refusing to store an index record without .par2 extension",
			slog.String("dir", msg.dir),
			slog.String("par2_file", title))
	} else if msg.status == index.StatusMissing {
		// If directory is missing (StatusMissing), don't update LastVerified.
		// Both writes below are ignored-with-log: the UI already shows the
		// final status, only the persistence is lost
		err := m.app.store.UpdateStatus(msg.dir, title, msg.status)
		if err != nil {
			m.app.logStoreError("failed to update index status in database", err)
		}
	} else {
		err := m.app.store.Put(msg.dir, title, msg.status, m.app.now())
		if err != nil {
			m.app.logStoreError("failed to save index to database", err)
		}
	}

	// The entry may be gone if the directory changed while the
	// verification was running; StatusMissing keeps the old LastVerified —
	// the index was not actually verified
	m.updateIndexItem(title, func(ii *indexListItem) {
		ii.status = msg.status
		if msg.status != index.StatusMissing {
			ii.lastVerified = m.app.now()
		}
	})

	// Update bottom panel statistics
	m.refreshStats()

	return clearCmd
}

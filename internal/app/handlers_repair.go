package app

import (
	"log/slog"

	"partui/internal/index"

	tea "github.com/charmbracelet/bubbletea"
)

// handleRepairFinished persists the repair result, updates the index entry
// and the bottom panel. The message mapping mirrors handleVerifyFinished:
// a successful (or no-op) repair is an OK with a fresh LastVerified — the
// repair re-verified every file of the set against its description hashes —
// and a missing directory keeps the old LastVerified. A failed repair
// reports its reason through the error line and leaves the record Damaged.
func (m *Model) handleRepairFinished(msg indexRepairFinishedMsg) tea.Cmd {
	clearCmd := m.reportError("PAR2 repair failed", msg.err)

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
		if len(msg.repaired) > 0 {
			m.app.logger.Info("PAR2 repair rewrote files",
				slog.String("dir", msg.dir),
				slog.String("par2_file", title),
				slog.Any("files", msg.repaired))
		}
	}

	// The entry may be gone if the directory changed while the repair was
	// running; StatusMissing keeps the old LastVerified — the index was not
	// actually touched
	m.updateIndexItem(title, func(ii *indexListItem) {
		ii.status = msg.status
		if msg.status != index.StatusMissing {
			ii.lastVerified = m.app.now()
		}
	})

	// Update header statistics; one repair task is finished
	taskDone(&m.repairing)
	m.refreshStats()

	return clearCmd
}

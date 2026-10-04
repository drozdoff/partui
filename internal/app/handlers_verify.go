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
	if msg.err != nil {
		m.app.logger.Error("failed to verify PAR2 file",
			slog.String("dir", msg.dir),
			slog.String("par2_file", msg.par2Filename),
			slog.String("error", msg.err.Error()))
	}

	title := msg.par2Filename

	// If directory is missing (StatusMissing), don't update LastVerified
	if msg.status == index.StatusMissing {
		err := m.app.store.UpdateStatus(msg.dir, title, msg.status)
		if err != nil {
			m.app.logger.Warn("failed to update index status in database", slog.String("error", err.Error()))
		}
	} else {
		err := m.app.store.Put(msg.dir, title, msg.status, m.app.now())
		if err != nil {
			m.app.logger.Warn("failed to save index to database", slog.String("error", err.Error()))
		}
	}

	idx, item := findListItem(m.indexList.Items(), title, func(fi indexListItem) string { return fi.par2Filename })
	if idx != -1 {
		item.status = msg.status
		// Only update lastVerified if not StatusMissing
		if msg.status != index.StatusMissing {
			item.lastVerified = m.app.now()
		}
		item.desc = formatIndexItemDesc(item, m.indexListExpanded)
		m.indexList.SetItem(idx, item)
	}

	// Update bottom panel statistics
	m.bottomPanel = m.app.calculateBottomPanelStats()

	return nil
}

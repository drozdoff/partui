package app

import (
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// errorVisibleFor is how long an error reported via reportError stays on
// screen before the clearErrorMsg tick removes it
const errorVisibleFor = 5 * time.Second

// reportError logs an operation failure and shows it in the bottom panel
// for errorVisibleFor. The returned command clears the line when the time
// is up; callers pass it through their own tea.Cmd return value. A nil
// error is ignored, so callers do not need their own nil checks.
func (m *Model) reportError(scope string, err error) tea.Cmd {
	if err == nil {
		return nil
	}
	m.app.logger.Error(scope, slog.String("error", err.Error()))
	m.errText = fmt.Sprintf("%s: %v", scope, err)
	m.errUntil = m.app.now().Add(errorVisibleFor)
	return tea.Tick(errorVisibleFor, func(time.Time) tea.Msg { return clearErrorMsg{} })
}

// handleClearErrorMsg removes the error line once its display time is up
func (m *Model) handleClearErrorMsg() tea.Cmd {
	m.errText = ""
	m.errUntil = time.Time{}
	return nil
}

package app

import (
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Error handling policy (plan item 4.1). Every failure in internal/ is
// classified as one of three levels; there is no separate ErrorHandler type
// — its roles are taken by reportError (reportable), logStoreError and the
// plain logger (ignored with a log) and by Run/run (fatal).
//
//	Level              When                                          Action
//	-----------------  --------------------------------------------  --------------------------------------
//	fatal              continuing is impossible: no log file, no     log Error → stderr → exit(1);
//	                   start directory, the UI itself failed         only in Run/run, before the UI starts
//	reportable         database operations, PAR2 create/verify,      log + red error line in the UI via
//	                   directory reads during navigation             reportError; the model state is
//	                                                                 rolled back (the item leaves
//	                                                                 processing)
//	ignored with       soft degradations: a status save or a         log Warn, work continues, the
//	a log              cleanup pass failed                           status does not change
//
// Selection rule: if a failure breaks a model invariant (an item would stay
// in processing forever), the reportable level is mandatory; if only
// statistics or persistence did not happen, a log entry is enough.
//
// Worker goroutines report their outcome exclusively via tea.Msg
// (parCreateErrorMsg, indexVerifyFinishedMsg): the handler is the single
// place that both shows the error line and restores the model state.

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

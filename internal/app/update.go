package app

// Dependencies used by the Update handlers live on the model's App (m.app):
// - m.app.wg: sync.WaitGroup - tracks background goroutines
// - m.app.store: IndexStore - index persistence
// - m.app.logger: *slog.Logger - structured logging
// - m.app.program: *tea.Program - receives messages from worker goroutines
//
// Update only dispatches: key handling lives in keys.go, the handlers for
// goroutine completion messages in handlers_create.go / handlers_verify.go.

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, m.handleWindowSize(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case parCreateFinishedMsg:
		return m, m.handleParCreateFinished(msg)
	case parCreateErrorMsg:
		return m, m.handleParCreateError(msg)
	case indexVerifyFinishedMsg:
		return m, m.handleVerifyFinished(msg)
	case clearErrorMsg:
		return m, m.handleClearErrorMsg()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	}

	return m, nil
}

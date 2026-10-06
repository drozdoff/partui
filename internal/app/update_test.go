package app

import (
	"errors"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestModel() *Model {
	m := NewModel(newTestApp())
	m.focus = FocusFile
	m.width = 80
	m.height = 24

	// Create fileList with proper delegate
	fileItems := []list.Item{}
	m.fileList = list.New(fileItems, newFileListItemDelegate(m), 0, 0)
	m.fileList.SetShowHelp(false)
	m.fileList.SetShowStatusBar(false)
	m.fileList.SetFilterState(list.Unfiltered)
	m.fileList.DisableQuitKeybindings()

	// Create indexList with proper delegate
	indexItems := []list.Item{}
	m.indexList = list.New(indexItems, newIndexListItemDelegate(m), 0, 0)
	m.indexList.SetShowHelp(false)
	m.indexList.SetShowStatusBar(false)
	m.indexList.SetFilterState(list.Unfiltered)
	m.indexList.DisableQuitKeybindings()

	return m
}

func TestUpdate_QuitCtrlC(t *testing.T) {
	m := newTestModel()

	// Test ctrl+c should return a quit command (not nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.NotNil(t, cmd)
}

func TestUpdate_TabSwitchesFocus(t *testing.T) {
	m := newTestModel()

	// Start with FocusFile
	assert.Equal(t, FocusFile, m.focus)

	// Tab should switch to FocusIndex
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, FocusIndex, newModel.(*Model).focus)

	// Tab should switch back to FocusFile
	newModel, _ = newModel.(*Model).Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, FocusFile, newModel.(*Model).focus)
}

func TestUpdate_NavigationUpKey(t *testing.T) {
	m := newTestModel()

	// Test up key - should not panic and return nil cmd
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Nil(t, cmd)
}

func TestUpdate_NavigationDownKey(t *testing.T) {
	m := newTestModel()

	// Test down key
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Nil(t, cmd)
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := newTestModel()

	msg := tea.WindowSizeMsg{Width: 100, Height: 50}
	newModel, cmd := m.Update(msg)

	resultModel := newModel.(*Model)
	assert.Equal(t, 100, resultModel.width)
	assert.Equal(t, 50, resultModel.height)
	assert.Nil(t, cmd)
}

func TestUpdate_EscapeKey(t *testing.T) {
	m := newTestModel()

	// Escape key should return nil cmd
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Nil(t, cmd)

	// Focus should remain unchanged
	assert.Equal(t, FocusFile, m.focus)
}

func TestUpdate_SpaceKey_NotInFileList(t *testing.T) {
	m := newTestModel()
	m.focus = FocusIndex

	// Space key should be ignored when not in file list
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	assert.Nil(t, cmd)
}

func TestUpdate_EnterKey_NotInFileList(t *testing.T) {
	m := newTestModel()
	m.focus = FocusIndex

	// Enter key should be ignored when not in file list
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Nil(t, cmd)
}

func TestUpdate_EnterKey_InFileList_NonDirectory(t *testing.T) {
	m := newTestModel()
	m.focus = FocusFile
	m.fileList.SetItems([]list.Item{
		fileListItem{title: "test.txt", isDir: false},
	})

	// Enter key on non-directory item - should handle gracefully
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Nil(t, cmd)
}

func TestUpdate_PgUpKey(t *testing.T) {
	m := newTestModel()

	// pgup
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	assert.Nil(t, cmd)
}

func TestUpdate_PgDownKey(t *testing.T) {
	m := newTestModel()

	// pgdown
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	assert.Nil(t, cmd)
}

func TestUpdate_HomeKey(t *testing.T) {
	m := newTestModel()

	// home
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	assert.Nil(t, cmd)
}

func TestUpdate_EndKey(t *testing.T) {
	m := newTestModel()

	// end
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	assert.Nil(t, cmd)
}

func TestUpdate_SpinnerTickMsg(t *testing.T) {
	m := newTestModel()

	// Spinner tick should pass through to fileList
	_, cmd := m.Update(spinner.TickMsg{})
	assert.Nil(t, cmd)
}

func TestUpdate_UnknownMsg(t *testing.T) {
	m := newTestModel()

	// Unknown message type should return nil cmd
	_, cmd := m.Update("unknown message")
	assert.Nil(t, cmd)
}

func TestUpdate_ModelReturnsSelf(t *testing.T) {
	m := newTestModel()

	// The Update method should return the same model pointer
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Same(t, m, newModel)
}

// Regression tests for item 1.2: key handlers must not panic on empty lists.

func TestUpdate_SpaceKey_EmptyList_NoPanic(t *testing.T) {
	m := newTestModel()

	assert.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	})
	assert.Equal(t, 0, m.filesSelected)
}

func TestUpdate_SpaceKey_SelectsFile(t *testing.T) {
	m := newTestModel()
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt"}})

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	it := m.fileList.Items()[0].(fileListItem)
	assert.True(t, it.selected)
	assert.Equal(t, 1, m.filesSelected)
}

func TestUpdate_CreateKey_EmptyList_NoPanic(t *testing.T) {
	m := newTestModel()

	assert.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	})
	assert.Empty(t, m.indexList.Items())
}

func TestUpdate_CreateKey_SingleFile_GoroutineExitsOnCancelledContext(t *testing.T) {
	m := newTestModel()
	m.app.cancel() // the launched goroutine must exit without doing work
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", size: 10}})

	assert.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	})

	it := m.fileList.Items()[0].(fileListItem)
	assert.True(t, it.processing)
	assert.Len(t, m.indexList.Items(), 1)

	// no goroutine may outlive the test
	m.app.wg.Wait()
}

func TestUpdate_VerifyKey_EmptyList_NoPanic(t *testing.T) {
	m := newTestModel()
	m.focus = FocusIndex

	assert.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	})
}

func TestUpdate_VerifyKey_OnIndex_MarksProcessing(t *testing.T) {
	m := newTestModel()
	m.app.cancel() // the launched goroutine must exit without doing work
	m.focus = FocusIndex
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.txt.par2", dir: t.TempDir(), status: index.StatusOK},
	})

	assert.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	})

	it := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusProcessing, it.status)

	// no goroutine may outlive the test
	m.app.wg.Wait()
}

func TestUpdate_EnterKey_EmptyList_NoPanic(t *testing.T) {
	m := newTestModel()

	assert.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	})
}

// Regression test for item 1.3: without a store (degraded mode) the
// completion messages must not panic.
func TestUpdate_Messages_WithoutDB_NoPanic(t *testing.T) {
	m := newTestModel()
	m.app.store = newNoopStore(m.app.logger)

	assert.NotPanics(t, func() {
		_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})
		_, _ = m.Update(parCreateErrorMsg{dir: "/tmp/x", title: "a.txt", err: errors.New("boom")})
		_, _ = m.Update(indexVerifyFinishedMsg{dir: "/tmp/x", par2Filename: "a.txt.par2", status: index.StatusOK})
		_, _ = m.Update(indexVerifyFinishedMsg{dir: "/tmp/x", par2Filename: "a.txt.par2", status: index.StatusMissing})
	})

	// degraded mode must be visible in the header
	assert.ErrorIs(t, m.header.storeErr, ErrStoreUnavailable)
}

// Regression: the placeholder added by `c` carries the directory name, and
// the finished message recomputes its meta from the new status and the
// fresh LastVerified. The description used to be computed before the status
// update, freezing the text on "Processing" forever.
func TestUpdate_CreateThenFinished_UpdatesStatusText(t *testing.T) {
	m := newTestModel()
	m.app.cancel() // the goroutine must exit without work
	m.currentDir = "/tmp/x"
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", size: 10}})

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	ph := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusProcessing, ph.status)
	assert.Equal(t, "/tmp/x", ph.dir)
	// a fresh placeholder has no verification time: no meta in the
	// collapsed mode
	assert.Empty(t, ph.desc)

	_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})

	it := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusOK, it.status)
	assert.Equal(t, "just now", it.desc)
	m.app.wg.Wait()
}

// Item 2.1 acceptance: the create path goes through the injected
// Par2Runner, not through gopar directly.
func TestUpdate_CreateKey_CallsPar2Runner(t *testing.T) {
	m := newTestModel()
	app := m.app
	app.program = tea.NewProgram(m, tea.WithContext(app.ctx))
	m.currentDir = "/tmp/x"
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", size: 64000}})

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	runner := app.par2.(*fakePar2)
	select {
	case parPath := <-runner.createdCh:
		assert.Equal(t, "/tmp/x/a.txt.par2", parPath)
	case <-time.After(2 * time.Second):
		t.Fatal("Par2Runner.Create was not called")
	}

	// the goroutine is parked sending the finished message to the idle
	// program; cancel the context so it exits and wg.Wait returns
	app.cancel()
	app.wg.Wait()
}

// Item 2.1 acceptance: the verify path goes through the injected
// Par2Runner as well.
func TestUpdate_VerifyKey_CallsPar2Runner(t *testing.T) {
	m := newTestModel()
	app := m.app
	app.program = tea.NewProgram(m, tea.WithContext(app.ctx))
	m.focus = FocusIndex
	dir := t.TempDir() // must exist: verify checks the directory first
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.txt.par2", dir: dir, status: index.StatusOK},
	})

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})

	runner := app.par2.(*fakePar2)
	select {
	case parPath := <-runner.verifiedCh:
		assert.Equal(t, dir+"/a.txt.par2", parPath)
	case <-time.After(2 * time.Second):
		t.Fatal("Par2Runner.Verify was not called")
	}

	app.cancel()
	app.wg.Wait()
}

// Item 3.2: an error reported via reportError is shown in the UI for
// errorVisibleFor and then removed by the clearErrorMsg tick.
func TestUpdate_ReportError_ShowsAndClears(t *testing.T) {
	m := newTestModel()
	fixed := time.Unix(1700000000, 0)
	m.app.clock = func() time.Time { return fixed }

	cmd := m.reportError("boom", errors.New("broken"))
	require.NotNil(t, cmd, "a tick must be scheduled to clear the error")
	assert.Equal(t, "boom: broken", m.errText)
	assert.Equal(t, fixed.Add(errorVisibleFor), m.errUntil)
	assert.Contains(t, m.View(), "boom: broken")

	_ = m.handleClearErrorMsg()
	assert.Empty(t, m.errText)
	assert.True(t, m.errUntil.IsZero())
	assert.NotContains(t, m.View(), "boom: broken")
}

// a nil error is not an error: nothing is shown and no tick is scheduled
func TestModel_ReportError_NilError_Noop(t *testing.T) {
	m := newTestModel()

	assert.Nil(t, m.reportError("scope", nil))
	assert.Empty(t, m.errText)
}

package app

import (
	"errors"
	"path/filepath"
	"testing"

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

// Regression test for item 1.3: without a database (degraded mode) the
// completion messages must not panic.
func TestUpdate_Messages_WithoutDB_NoPanic(t *testing.T) {
	m := newTestModel()

	assert.NotPanics(t, func() {
		_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})
		_, _ = m.Update(parCreateErrorMsg{dir: "/tmp/x", title: "a.txt", err: errors.New("boom")})
		_, _ = m.Update(indexVerifyFinishedMsg{dir: "/tmp/x", par2Filename: "a.txt.par2", status: index.StatusOK})
		_, _ = m.Update(indexVerifyFinishedMsg{dir: "/tmp/x", par2Filename: "a.txt.par2", status: index.StatusMissing})
	})

	// degraded mode must be visible in the bottom panel
	assert.ErrorIs(t, m.bottomPanel.storeErr, ErrStoreUnavailable)
}

func TestUpdate_ParCreateFinished_UpdatesItems(t *testing.T) {
	m := newTestModel()
	m.currentDir = "/tmp/x"
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", processing: true}})
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.txt.par2", dir: "/tmp/x", status: index.StatusProcessing},
	})

	_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})

	fit := m.fileList.Items()[0].(fileListItem)
	assert.False(t, fit.processing)
	assert.True(t, fit.archive)

	iit := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusOK, iit.status)
}

// Regression test: parCreateFinishedMsg must store the index record under
// the .par2 file name, not the source file name.
func TestUpdate_ParCreateFinished_StoresPar2NameInDB(t *testing.T) {
	db, err := index.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	app := newTestApp()
	app.db = db
	defer app.Shutdown()

	m := NewModel(app)
	m.currentDir = "/tmp/x"

	_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})

	desc, err := index.GetIndex(db, "/tmp/x", "a.txt.par2")
	assert.NoError(t, err)
	require.NotNil(t, desc, "index record must be stored under the .par2 name")
	assert.Equal(t, index.StatusOK, desc.Status)

	junk, err := index.GetIndex(db, "/tmp/x", "a.txt")
	assert.NoError(t, err)
	assert.Nil(t, junk, "no record must be stored under the source file name")
}

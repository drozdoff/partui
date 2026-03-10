package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func newTestModel() *Model {
	m := &Model{
		focus:  FocusFile,
		width:  80,
		height: 24,
	}

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

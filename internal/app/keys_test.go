package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyDir creates a directory with two files and a subdirectory
func keyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.bin"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	return dir
}

func pressKey(m *Model, key string) {
	var msg tea.KeyMsg
	switch key {
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	_, _ = m.Update(msg)
}

// TestKeys_Table covers the branches of handleKeyFile, handleKeyIndex and
// handleKeyExpanded
func TestKeys_Table(t *testing.T) {
	fixed := time.Unix(1700000000, 0)

	tests := []struct {
		name  string
		setup func(t *testing.T, m *Model)
		key   string
		check func(t *testing.T, m *Model)
	}{
		{
			name: "space on a file selects it and bumps the counter",
			setup: func(t *testing.T, m *Model) {
				m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt"}})
			},
			key: " ",
			check: func(t *testing.T, m *Model) {
				it := m.fileList.Items()[0].(fileListItem)
				assert.True(t, it.selected)
				assert.Equal(t, 1, m.filesSelected)
			},
		},
		{
			name: "space on a directory does nothing",
			setup: func(t *testing.T, m *Model) {
				m.fileList.SetItems([]list.Item{fileListItem{title: "sub/", isDir: true}})
			},
			key: " ",
			check: func(t *testing.T, m *Model) {
				it := m.fileList.Items()[0].(fileListItem)
				assert.False(t, it.selected)
				assert.Equal(t, 0, m.filesSelected)
			},
		},
		{
			name: "space on an archived file does nothing",
			setup: func(t *testing.T, m *Model) {
				m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", archive: true}})
			},
			key: " ",
			check: func(t *testing.T, m *Model) {
				it := m.fileList.Items()[0].(fileListItem)
				assert.False(t, it.selected)
				assert.Equal(t, 0, m.filesSelected)
			},
		},
		{
			name: "space in an empty list does not panic",
			key:  " ",
			check: func(t *testing.T, m *Model) {
				assert.Equal(t, 0, m.filesSelected)
			},
		},
		{
			name: "c with no selection processes the item under the cursor",
			setup: func(t *testing.T, m *Model) {
				m.app.cancel() // the goroutine must exit without work
				m.currentDir = "/tmp/x"
				m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", size: 10}})
			},
			key: "c",
			check: func(t *testing.T, m *Model) {
				it := m.fileList.Items()[0].(fileListItem)
				assert.True(t, it.processing)
				require.Len(t, m.indexList.Items(), 1)
				m.app.wg.Wait()
			},
		},
		{
			name: "c with N selected files adds N index entries",
			setup: func(t *testing.T, m *Model) {
				m.app.cancel()
				m.currentDir = "/tmp/x"
				m.fileList.SetItems([]list.Item{
					fileListItem{title: "a.txt", selected: true},
					fileListItem{title: "b.bin", selected: true},
				})
				m.filesSelected = 2
			},
			key: "c",
			check: func(t *testing.T, m *Model) {
				require.Len(t, m.indexList.Items(), 2)
				m.app.wg.Wait()
			},
		},
		{
			name: "c in an empty list does not panic",
			key:  "c",
			check: func(t *testing.T, m *Model) {
				assert.Empty(t, m.indexList.Items())
			},
		},
		{
			name: "c on a directory does nothing",
			setup: func(t *testing.T, m *Model) {
				m.app.cancel()
				m.currentDir = "/tmp/x"
				m.fileList.SetItems([]list.Item{fileListItem{title: "sub/", isDir: true}})
			},
			key: "c",
			check: func(t *testing.T, m *Model) {
				it := m.fileList.Items()[0].(fileListItem)
				assert.False(t, it.processing)
				assert.Empty(t, m.indexList.Items())
			},
		},
		{
			name: "v moves the index to processing and writes the status",
			setup: func(t *testing.T, m *Model) {
				m.app.cancel()
				m.focus = FocusIndex
				require.NoError(t, m.app.store.Put("/d", "a.txt.par2", index.StatusOK, fixed))
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
				})
			},
			key: "v",
			check: func(t *testing.T, m *Model) {
				it := m.indexList.Items()[0].(indexListItem)
				assert.Equal(t, index.StatusProcessing, it.status)

				desc, err := m.app.store.Get("/d", "a.txt.par2")
				require.NoError(t, err)
				require.NotNil(t, desc)
				assert.Equal(t, index.StatusProcessing, desc.Status)
				m.app.wg.Wait()
			},
		},
		{
			name:  "v in an empty index list does not panic",
			setup: func(t *testing.T, m *Model) { m.focus = FocusIndex },
			key:   "v",
			check: func(t *testing.T, m *Model) {},
		},
		{
			name: "v is ignored while the file list has focus",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusFile
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
				})
			},
			key: "v",
			check: func(t *testing.T, m *Model) {
				it := m.indexList.Items()[0].(indexListItem)
				assert.Equal(t, index.StatusOK, it.status)
			},
		},
		{
			name: "enter on a directory rebuilds the model",
			setup: func(t *testing.T, m *Model) {
				m.currentDir = keyDir(t)
				m.fileList.SetItems([]list.Item{fileListItem{title: "sub/", isDir: true}})
			},
			key: "enter",
			check: func(t *testing.T, m *Model) {
				assert.Equal(t, "sub", filepath.Base(m.currentDir))
			},
		},
		{
			name: "i expands the index list from the database",
			setup: func(t *testing.T, m *Model) {
				require.NoError(t, m.app.store.Put("/d", "a.txt.par2", index.StatusOK, fixed))
				require.NoError(t, m.app.store.Put("/e", "b.bin.par2", index.StatusDamaged, fixed))
			},
			key: "i",
			check: func(t *testing.T, m *Model) {
				assert.True(t, m.indexListExpanded)
				assert.Equal(t, FocusIndex, m.focus)
				assert.Len(t, m.indexList.Items(), 2)
			},
		},
		{
			name: "esc collapses back to local indexes",
			setup: func(t *testing.T, m *Model) {
				m.currentDir = keyDir(t)
				m.indexListExpanded = true
			},
			key: "esc",
			check: func(t *testing.T, m *Model) {
				assert.False(t, m.indexListExpanded)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			if tc.setup != nil {
				tc.setup(t, m)
			}
			assert.NotPanics(t, func() { pressKey(m, tc.key) })
			tc.check(t, m)
		})
	}
}

func TestKeys_Enter_OnParent_RestoresCursor(t *testing.T) {
	root := keyDir(t)

	m := newTestModel()
	// start inside the subdirectory; the only entry is "../"
	require.NoError(t, m.buildModelForDirectory(filepath.Join(root, "sub")))

	pressKey(m, "enter")

	// back in the parent directory with the cursor on the "sub/" entry
	// we came from — the first-entry regression from item 1.2
	assert.Equal(t, root, m.currentDir)
	it, ok := m.fileList.SelectedItem().(fileListItem)
	require.True(t, ok)
	assert.Equal(t, "sub/", it.title)
}

func TestKeys_Enter_OnFile_DoesNothing(t *testing.T) {
	dir := keyDir(t)

	m := newTestModel()
	require.NoError(t, m.buildModelForDirectory(dir))
	// cursor on a.txt: "../", "sub/", "a.txt", "b.bin"
	m.fileList.Select(2)

	pressKey(m, "enter")

	assert.Equal(t, dir, m.currentDir)
}

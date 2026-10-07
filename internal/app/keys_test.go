package app

import (
	"fmt"
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
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		msg = tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		msg = tea.KeyMsg{Type: tea.KeyPgDown}
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
			name: "v on a record without .par2 extension is refused",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusIndex
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "style.css", dir: "/d", status: index.StatusOK},
				})
			},
			key: "v",
			check: func(t *testing.T, m *Model) {
				// the entry must not enter processing, nothing is written
				it := m.indexList.Items()[0].(indexListItem)
				assert.Equal(t, index.StatusOK, it.status)

				desc, err := m.app.store.Get("/d", "style.css")
				require.NoError(t, err)
				assert.Nil(t, desc)
			},
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
		{
			name: "v in the expanded list verifies the selected index",
			setup: func(t *testing.T, m *Model) {
				m.app.cancel()
				m.indexListExpanded = true
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
			name: "an unknown key in the expanded list does nothing",
			setup: func(t *testing.T, m *Model) {
				m.indexListExpanded = true
				m.focus = FocusIndex
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
				})
			},
			key: "x",
			check: func(t *testing.T, m *Model) {
				it := m.indexList.Items()[0].(indexListItem)
				assert.Equal(t, index.StatusOK, it.status)
			},
		},
		{
			name: "d opens the confirmation with Отмена as the default",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusIndex
				require.NoError(t, m.app.store.Put("/d", "a.txt.par2", index.StatusOK, fixed))
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
				})
			},
			key: "d",
			check: func(t *testing.T, m *Model) {
				require.NotNil(t, m.confirm)
				assert.Equal(t, confirmCancel, m.confirm.selected)
				assert.Equal(t, "a.txt.par2", m.confirm.item.par2Filename)

				// nothing is deleted until Enter on "Удалить"
				assert.Len(t, m.indexList.Items(), 1)
				desc, err := m.app.store.Get("/d", "a.txt.par2")
				require.NoError(t, err)
				assert.NotNil(t, desc)
			},
		},
		{
			name: "d is ignored while the file list has focus",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusFile
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
				})
			},
			key: "d",
			check: func(t *testing.T, m *Model) {
				assert.Nil(t, m.confirm)
			},
		},
		{
			name: "d on a processing index is refused",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusIndex
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusProcessing},
				})
			},
			key: "d",
			check: func(t *testing.T, m *Model) {
				assert.Nil(t, m.confirm)
			},
		},
		{
			name: "enter on the default Отмена keeps the record",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusIndex
				require.NoError(t, m.app.store.Put("/d", "a.txt.par2", index.StatusOK, fixed))
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
				})
				m.confirm = &confirmDialog{
					item:     indexListItem{par2Filename: "a.txt.par2", dir: "/d"},
					selected: confirmCancel,
				}
			},
			key: "enter",
			check: func(t *testing.T, m *Model) {
				assert.Nil(t, m.confirm)
				assert.Len(t, m.indexList.Items(), 1)
				desc, err := m.app.store.Get("/d", "a.txt.par2")
				require.NoError(t, err)
				assert.NotNil(t, desc)
			},
		},
		{
			name: "enter on Удалить removes the record and the entry",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusIndex
				require.NoError(t, m.app.store.Put("/d", "a.txt.par2", index.StatusOK, fixed))
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
					indexListItem{par2Filename: "b.bin.par2", dir: "/d", status: index.StatusOK},
				})
				m.confirm = &confirmDialog{
					item:     indexListItem{par2Filename: "a.txt.par2", dir: "/d"},
					selected: confirmDelete,
				}
			},
			key: "enter",
			check: func(t *testing.T, m *Model) {
				assert.Nil(t, m.confirm)

				desc, err := m.app.store.Get("/d", "a.txt.par2")
				require.NoError(t, err)
				assert.Nil(t, desc)

				// the neighbour entry stays
				require.Len(t, m.indexList.Items(), 1)
				it := m.indexList.Items()[0].(indexListItem)
				assert.Equal(t, "b.bin.par2", it.par2Filename)
			},
		},
		{
			name: "esc closes the dialog and keeps the record",
			setup: func(t *testing.T, m *Model) {
				m.focus = FocusIndex
				require.NoError(t, m.app.store.Put("/d", "a.txt.par2", index.StatusOK, fixed))
				m.confirm = &confirmDialog{
					item:     indexListItem{par2Filename: "a.txt.par2", dir: "/d"},
					selected: confirmDelete,
				}
			},
			key: "esc",
			check: func(t *testing.T, m *Model) {
				assert.Nil(t, m.confirm)
				desc, err := m.app.store.Get("/d", "a.txt.par2")
				require.NoError(t, err)
				assert.NotNil(t, desc)
			},
		},
		{
			name: "keys do not reach the lists while the dialog is open",
			setup: func(t *testing.T, m *Model) {
				m.indexList.SetItems([]list.Item{
					indexListItem{par2Filename: "a.txt.par2", dir: "/d", status: index.StatusOK},
					indexListItem{par2Filename: "b.bin.par2", dir: "/d", status: index.StatusOK},
					indexListItem{par2Filename: "c.par2", dir: "/d", status: index.StatusOK},
				})
				m.confirm = &confirmDialog{
					item:     indexListItem{par2Filename: "a.txt.par2", dir: "/d"},
					selected: confirmCancel,
				}
			},
			key: "down",
			check: func(t *testing.T, m *Model) {
				// the dialog handled the key, the list cursor did not move
				assert.Equal(t, confirmDelete, m.confirm.selected)
				assert.Equal(t, 0, m.indexList.Index())
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

// The full confirmation flow: 'd' opens the dialog with "Отмена" as the
// default, arrows toggle the choice, Enter applies it
func TestDeleteConfirm_Flow(t *testing.T) {
	m := newTestModel()
	m.focus = FocusIndex
	require.NoError(t, m.app.store.Put("/d", "a.par2", index.StatusOK, time.Unix(1700000000, 0)))
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.par2", dir: "/d", status: index.StatusOK},
	})

	pressKey(m, "d")
	require.NotNil(t, m.confirm)
	assert.Equal(t, confirmCancel, m.confirm.selected)

	// up twice: Отмена → Удалить → Отмена
	pressKey(m, "up")
	require.Equal(t, confirmDelete, m.confirm.selected)
	pressKey(m, "up")
	require.Equal(t, confirmCancel, m.confirm.selected)

	pressKey(m, "enter")

	// cancelled: the record and the entry are still there
	desc, err := m.app.store.Get("/d", "a.par2")
	require.NoError(t, err)
	assert.NotNil(t, desc)
	assert.Len(t, m.indexList.Items(), 1)

	// again, this time through "Удалить"
	pressKey(m, "d")
	require.NotNil(t, m.confirm)
	pressKey(m, "down")
	pressKey(m, "enter")

	assert.Nil(t, m.confirm)
	desc, err = m.app.store.Get("/d", "a.par2")
	require.NoError(t, err)
	assert.Nil(t, desc)
	assert.Empty(t, m.indexList.Items())
}

func TestKeys_Enter_OnParent_RestoresCursor(t *testing.T) {
	root := keyDir(t)

	m := newTestModel()
	// start inside the subdirectory; the only entry is "../"
	_, err := m.buildModelForDirectory(filepath.Join(root, "sub"))
	require.NoError(t, err)

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
	_, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	// cursor on a.txt: "../", "sub/", "a.txt", "b.bin"
	m.fileList.Select(2)

	pressKey(m, "enter")

	assert.Equal(t, dir, m.currentDir)
}

// A one-page list has no page keys in bubbles — pgup/pgdown are disabled
// there — so pgup jumps to the first entry and pgdown to the last one
func TestKeys_PgKeys_OnSinglePageList(t *testing.T) {
	m := newTestModel()
	items := make([]list.Item, 0, 5)
	for _, name := range []string{"../", "a.txt", "b.txt", "c.txt", "d.txt"} {
		items = append(items, fileListItem{title: name})
	}
	m.fileList.SetItems(items)
	m.applyLayout()
	require.Equal(t, 1, m.fileList.Paginator.TotalPages,
		"the fixture must fit on one page")

	// pgup from the middle jumps to the first entry
	m.fileList.Select(2)
	pressKey(m, "pgup")
	assert.Equal(t, 0, m.fileList.Index())

	// pgdown jumps to the last entry
	pressKey(m, "pgdown")
	assert.Equal(t, 4, m.fileList.Index())

	// pgup at the very top stays
	pressKey(m, "pgup")
	assert.Equal(t, 0, m.fileList.Index())

	// pgdown at the last entry stays
	pressKey(m, "pgdown")
	assert.Equal(t, 4, m.fileList.Index())
}

// Regression: the one-page branch of navigateList must swallow only
// pgup/pgdown — up/down/home/end still reach the list (a previous version
// dropped them, killing the arrows in every short directory)
func TestKeys_ArrowsAndHomeEnd_OnSinglePageList(t *testing.T) {
	m := newTestModel()
	items := make([]list.Item, 0, 5)
	for _, name := range []string{"../", "a.txt", "b.txt", "c.txt", "d.txt"} {
		items = append(items, fileListItem{title: name})
	}
	m.fileList.SetItems(items)
	m.applyLayout()
	require.Equal(t, 1, m.fileList.Paginator.TotalPages,
		"the fixture must fit on one page")

	// down moves the cursor, up moves it back
	pressKey(m, "down")
	assert.Equal(t, 1, m.fileList.Index())
	pressKey(m, "up")
	assert.Equal(t, 0, m.fileList.Index())

	// end jumps to the last entry, home back to the first
	pressKey(m, "end")
	assert.Equal(t, 4, m.fileList.Index())
	pressKey(m, "home")
	assert.Equal(t, 0, m.fileList.Index())
}

// A longer list keeps the bubbles behavior: pgup/pgdown turn the pages
func TestKeys_PgKeys_OnMultiPageList_KeepPaging(t *testing.T) {
	m := newTestModel()
	items := make([]list.Item, 0, 64)
	for i := range 64 {
		items = append(items, fileListItem{title: fmt.Sprintf("f%02d.txt", i)})
	}
	m.fileList.SetItems(items)
	m.applyLayout()
	require.Greater(t, m.fileList.Paginator.TotalPages, 1,
		"the fixture must span several pages")

	pressKey(m, "pgdown")
	assert.Equal(t, 1, m.fileList.Paginator.Page, "pgdown must turn the page")

	pressKey(m, "pgup")
	assert.Equal(t, 0, m.fileList.Paginator.Page, "pgup must turn back")
}

// Item 3.3: updateIndexItem mutates the entry, recomputes the description
// and reports missing entries instead of silently doing nothing
func TestModel_UpdateIndexItem(t *testing.T) {
	m := newTestModel()
	fixed := time.Unix(1700000000, 0)
	m.app.clock = func() time.Time { return fixed }
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.par2", dir: "/d", status: index.StatusProcessing},
	})

	assert.True(t, m.updateIndexItem("a.par2", func(ii *indexListItem) {
		ii.status = index.StatusOK
		ii.lastVerified = fixed
	}))

	it := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusOK, it.status)
	assert.Equal(t, "just now", it.desc)

	assert.False(t, m.updateIndexItem("missing.par2", func(ii *indexListItem) {}))
}

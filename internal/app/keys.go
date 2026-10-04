package app

import (
	"log/slog"
	"os"
	"partui/internal/index"
	"path/filepath"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// handleKeyMsg splits key handling into a common layer (quit, focus toggle,
// navigation, expanding the index list) and per-focus layers
func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		return m, m.toggleFocus()
	case "up", "down", "pgup", "pgdown", "home", "end":
		// Navigate the focused list
		var cmd tea.Cmd
		if m.focus == FocusFile {
			m.fileList, cmd = m.fileList.Update(msg)
		} else {
			m.indexList, cmd = m.indexList.Update(msg)
		}
		return m, cmd
	case "i":
		// The index list expands from any focus
		if !m.indexListExpanded {
			return m, m.expandIndexList()
		}
		return m, nil
	}

	if m.indexListExpanded {
		return m.handleKeyExpanded(msg)
	}
	if m.focus == FocusFile {
		return m.handleKeyFile(msg)
	}
	return m.handleKeyIndex(msg)
}

// toggleFocus switches the active list
func (m *Model) toggleFocus() tea.Cmd {
	if m.focus == FocusFile {
		m.focus = FocusIndex
	} else {
		m.focus = FocusFile
	}
	return nil
}

// handleKeyFile handles the keys of the file list
func (m *Model) handleKeyFile(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ":
		return m, m.toggleSelect()
	case "c":
		return m, m.createSelected()
	case "enter":
		return m, m.openSelected()
	}
	return m, nil
}

// handleKeyIndex handles the keys of the collapsed index list
func (m *Model) handleKeyIndex(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "v":
		return m, m.verifySelected()
	}
	return m, nil
}

// handleKeyExpanded handles the keys while the full index list from the
// database is shown
func (m *Model) handleKeyExpanded(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.collapseIndexList()
	case "v":
		return m, m.verifySelected()
	}
	return m, nil
}

// toggleSelect marks or unmarks the selected file and moves the cursor down;
// directories and already archived files are not selectable
func (m *Model) toggleSelect() tea.Cmd {
	item, ok := selectedFileItem(m)
	if !ok {
		return nil
	}
	if !item.isDir && !item.archive {
		if item.selected {
			m.filesSelected -= 1
		} else {
			m.filesSelected += 1
		}
		item.selected = !item.selected
		m.fileList.SetItem(m.fileList.Index(), item)
		m.fileList.CursorDown()
	}
	return nil
}

// createSelected starts PAR2 creation for every selected file, or for the
// file under the cursor when nothing is selected
func (m *Model) createSelected() tea.Cmd {
	// TODO: Now one index file per selected file is default.
	// override this behaviour in config??? how to specify index file name for a list of files???
	// needs some UI for config?
	// TODO: handle change dir during process. file list is now strictly for current dir.
	if m.filesSelected > 0 {
		for idx, i := range m.fileList.Items() {
			item, ok := i.(fileListItem)
			if !ok {
				continue
			}
			if item.selected {
				item.processing = true
				item.selected = false
				m.fileList.SetItem(idx, item)
				m.addIndex(item.title + ".par2")
				m.app.launch(func() {
					m.app.createParFiles(m.currentDir, item.title, item.size)
				})
			}
		}
	} else {
		item, ok := selectedFileItem(m)
		if !ok {
			return nil
		}
		// directories cannot be archived
		if item.isDir {
			return nil
		}
		item.processing = true
		m.fileList.SetItem(m.fileList.Index(), item)
		m.fileList.CursorDown()
		m.addIndex(item.title + ".par2")
		m.app.launch(func() {
			m.app.createParFiles(m.currentDir, item.title, item.size)
		})
	}
	return nil
}

// openSelected enters the selected directory; when leaving through "../"
// the cursor lands on the directory we came from
func (m *Model) openSelected() tea.Cmd {
	item, ok := selectedFileItem(m)
	if !ok {
		return nil
	}
	if !item.isDir {
		return nil
	}

	// the titles of directory entries carry a trailing slash; Join cleans
	// it off and resolves "../"
	newDir := filepath.Join(m.currentDir, item.title)

	// Save current dir to select when going back
	var prevDir string = ""
	if item.title == "../" {
		prevDir = m.currentDir
	}

	// A failed navigation keeps the model in the current directory and is
	// reported in the UI; only startup failures are fatal (plan item 4.1)
	if err := m.buildModelForDirectory(newDir); err != nil {
		return m.reportError("change directory", err)
	}
	// If going to parent, select the directory we came from
	if prevDir != "" {
		idx := findDirIndex(m.fileList.Items(), prevDir)
		if idx >= 0 {
			m.fileList.Select(idx)
		}
	}
	return nil
}

// verifySelected starts verification of the selected index; it requires the
// index list to have focus, also in the expanded mode
func (m *Model) verifySelected() tea.Cmd {
	if m.focus != FocusIndex {
		return nil
	}
	item, ok := selectedIndexChanged(m)
	if !ok {
		return nil
	}
	// only .par2 files can be verified; the expanded list may still
	// contain legacy records stored under source file names
	if !isPar2Name(item.par2Filename) {
		m.app.logger.Warn("refusing to verify a record without .par2 extension",
			slog.String("dir", item.dir),
			slog.String("par2_file", item.par2Filename))
		return nil
	}
	item.status = index.StatusProcessing
	item.desc = formatIndexItemDesc(item, m.indexListExpanded)
	m.indexList.SetItem(m.indexList.Index(), item)
	m.indexList.CursorDown()

	if err := m.app.store.UpdateStatus(item.dir, item.par2Filename, index.StatusProcessing); err != nil {
		m.app.logger.Warn("failed to update index status in database", slog.String("error", err.Error()))
	}
	m.refreshStats()
	m.app.launch(func() {
		m.app.verifyParFiles(item.dir, item)
	})
	return nil
}

// expandIndexList replaces the index list with all indexes from the database
func (m *Model) expandIndexList() tea.Cmd {
	// m.focus = FocusFile
	// TODO: i button toggle, do the same as for Esc
	m.indexListExpanded = true
	m.focus = FocusIndex
	m.rebuildIndexListAll()
	return nil
}

// rebuildIndexListAll refills the index list from the whole database —
// the expanded mode
func (m *Model) rebuildIndexListAll() {
	indexes, err := m.app.store.All()
	if err != nil {
		m.reportError("load all indexes", err)
		return
	}

	items := make([]list.Item, len(indexes))
	for i, idx := range indexes {
		indexItem := indexListItem{
			par2Filename: idx.Filename,
			dir:          idx.Dir,
			status:       idx.Status,
			lastVerified: idx.LastVerified,
		}
		indexItem.desc = formatIndexItemDesc(indexItem, m.indexListExpanded)
		items[i] = indexItem
	}

	m.setIndexList(items, "All Indexes from Database (Press Esc to return)")
}

// collapseIndexList rebuilds the index list from the local directory
func (m *Model) collapseIndexList() tea.Cmd {
	m.indexListExpanded = false
	m.rebuildIndexListLocal()
	return nil
}

// rebuildIndexListLocal refills the index list from the .par2 files of the
// current directory — the collapsed mode
func (m *Model) rebuildIndexListLocal() {
	osFiles, err := os.ReadDir(m.currentDir)
	if err != nil {
		m.reportError("read directory", err)
		return
	}
	m.setIndexList(m.processIndexFiles(osFiles), "Index Files")
}

// setIndexList replaces the index list with the given items under the
// given title and reapplies the layout
func (m *Model) setIndexList(items []list.Item, title string) {
	m.indexList = list.New(items, newIndexListItemDelegate(m), 0, 0)
	m.indexList.Title = title
	m.indexList.SetShowHelp(false)
	m.indexList.SetShowStatusBar(false)
	m.indexList.SetFilterState(list.Unfiltered)
	m.indexList.DisableQuitKeybindings()
	m.applyLayout()
}

// addIndex appends a placeholder entry for a .par2 file being created
func (m *Model) addIndex(par2Filename string) {
	item := indexListItem{
		par2Filename: par2Filename,
		dir:          m.currentDir,
		status:       index.StatusProcessing,
	}
	item.desc = formatIndexItemDesc(item, m.indexListExpanded)
	indexItems := m.indexList.Items()
	m.indexList.SetItems(append(indexItems, item))
}

// selectedFileItem returns the selected item of the file list and reports
// whether an item is actually selected. It returns false when the list is
// empty, the item has an unexpected type, or the list is being filtered —
// a filter changes the meaning of the selected index, so acting on the
// selection is unsafe.
func selectedFileItem(m *Model) (fileListItem, bool) {
	it, ok := m.fileList.SelectedItem().(fileListItem)
	return it, ok && !m.fileList.SettingFilter()
}

// selectedIndexChanged returns the selected item of the index list and
// reports whether an item is actually selected, with the same guarantees
// as selectedFileItem.
func selectedIndexChanged(m *Model) (indexListItem, bool) {
	it, ok := m.indexList.SelectedItem().(indexListItem)
	return it, ok && !m.indexList.SettingFilter()
}

// findListItem searches for an item with the given title in the list.
// Returns the index and the item, or -1 and a zero value if not found.
func findListItem[T any](items []list.Item, title string, getTitle func(T) string) (int, T) {
	for i, item := range items {
		typedItem, ok := item.(T)
		if !ok {
			continue
		}
		if getTitle(typedItem) == title {
			return i, typedItem
		}
	}
	// no item found is possible scenario if dir is changed while some files are still processing
	var zero T
	return -1, zero
}

// updateIndexItem finds an index list entry by the par2 file name, applies
// mutate, recomputes the description from the updated fields and repaints
// the list. It reports whether the entry was found — it may be gone if the
// directory changed while the task was running. The description is
// recomputed after the mutation because it renders the status text.
func (m *Model) updateIndexItem(par2Filename string, mutate func(*indexListItem)) bool {
	idx, item := findListItem(m.indexList.Items(), par2Filename, func(it indexListItem) string { return it.par2Filename })
	if idx == -1 {
		return false
	}
	mutate(&item)
	item.desc = formatIndexItemDesc(item, m.indexListExpanded)
	m.indexList.SetItem(idx, item)
	return true
}

// updateFileItem finds a file list entry by title, applies mutate and
// repaints the list, with the same not-found guarantee as updateIndexItem
func (m *Model) updateFileItem(title string, mutate func(*fileListItem)) bool {
	idx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
	if idx == -1 {
		return false
	}
	mutate(&item)
	m.fileList.SetItem(idx, item)
	return true
}

func findDirIndex(items []list.Item, dirName string) int {
	// dirName is full path, we need to find the directory entry
	// The entry title will be "dirname/"
	targetTitle := filepath.Base(dirName) + "/"
	for i, item := range items {
		fi, ok := item.(fileListItem)
		if !ok {
			continue
		}
		if fi.isDir && fi.title == targetTitle {
			return i
		}
	}
	// -1 also covers the first element correctly: the caller checks idx >= 0
	return -1
}

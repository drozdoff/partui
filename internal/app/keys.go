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
				m.app.wg.Add(1)
				go m.app.createParFiles(m.currentDir, item.title, item.size)
			}
		}
	} else {
		item, ok := selectedFileItem(m)
		if !ok {
			return nil
		}
		item.processing = true
		m.fileList.SetItem(m.fileList.Index(), item)
		m.fileList.CursorDown()
		m.addIndex(item.title + ".par2")
		m.app.wg.Add(1)
		go m.app.createParFiles(m.currentDir, item.title, item.size)
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

	dir := m.currentDir + "/" + item.title
	newDir := filepath.Dir(dir)

	// Save current dir to select when going back
	var prevDir string = ""
	if item.title == "../" {
		prevDir = m.currentDir
	}

	err := m.buildModelForDirectory(newDir)
	printShowStopper(m.app.logger, err)
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
	item.status = index.StatusProcessing
	item.desc = formatIndexItemDesc(item, m.indexListExpanded)
	m.indexList.SetItem(m.indexList.Index(), item)
	m.indexList.CursorDown()

	if err := m.app.store.UpdateStatus(item.dir, item.par2Filename, index.StatusProcessing); err != nil {
		m.app.logger.Warn("failed to update index status in database", slog.String("error", err.Error()))
	}
	m.bottomPanel = m.app.calculateBottomPanelStats()
	m.app.wg.Add(1)
	go m.app.verifyParFiles(item.dir, item)
	return nil
}

// expandIndexList replaces the index list with all indexes from the database
func (m *Model) expandIndexList() tea.Cmd {
	// m.focus = FocusFile
	// TODO: i button toggle, do the same as for Esc
	m.indexListExpanded = true
	m.focus = FocusIndex

	indexes, err := m.app.store.All()
	if err != nil {
		m.app.logger.Warn("failed to load all indexes", slog.String("error", err.Error()))
		return nil
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

	m.indexList = list.New(items, newIndexListItemDelegate(m), 0, 0)
	m.indexList.Title = "All Indexes from Database (Press Esc to return)"
	m.indexList.SetShowStatusBar(false)
	m.indexList.DisableQuitKeybindings()
	m.applyLayout()

	return nil
}

// collapseIndexList rebuilds the index list from the local directory
func (m *Model) collapseIndexList() tea.Cmd {
	m.indexListExpanded = false
	osFiles, err := os.ReadDir(m.currentDir)
	if err != nil {
		m.app.logger.Warn("failed to read directory",
			slog.String("dir", m.currentDir),
			slog.String("error", err.Error()))
		return nil
	}
	indexItems := m.processIndexFiles(osFiles)

	m.indexList = list.New(indexItems, newIndexListItemDelegate(m), 0, 0)
	m.indexList.Title = "Index Files"
	m.indexList.SetShowStatusBar(false)
	m.indexList.DisableQuitKeybindings()
	m.applyLayout()

	return nil
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

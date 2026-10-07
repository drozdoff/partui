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
	// the confirmation dialog is modal: it consumes every key, so the
	// lists cannot change between the 'd' press and the answer
	if m.confirm != nil {
		return m.handleKeyConfirm(msg)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		return m, m.toggleFocus()
	case "up", "down", "pgup", "pgdown", "home", "end":
		// Navigate the focused list
		var cmd tea.Cmd
		if m.focus == FocusFile {
			cmd = m.navigateList(&m.fileList, msg)
		} else {
			cmd = m.navigateList(&m.indexList, msg)
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

// navigateList routes one navigation key into the focused list. A list that
// fits on one page has no page keys in bubbles — pgup/pgdown are disabled
// there (TotalPages == 1) and would be dead — so they are handled here:
// pgup jumps to the first entry, pgdown to the last one. Every other
// navigation key — up/down/home/end — keeps the list behavior: on a
// one-page list those bindings stay enabled in bubbles.
func (m *Model) navigateList(l *list.Model, msg tea.KeyMsg) tea.Cmd {
	if l.Paginator.TotalPages > 1 {
		// list.Model.Update has a value receiver: the updated model comes
		// back as the result and must be written back (as handleKeyMsg did)
		updated, cmd := l.Update(msg)
		*l = updated
		return cmd
	}
	switch msg.String() {
	case "pgup":
		if l.Index() > 0 {
			l.Select(0)
		}
		return nil
	case "pgdown":
		if n := len(l.Items()); n > 0 && l.Index() < n-1 {
			l.Select(n - 1)
		}
		return nil
	}
	updated, cmd := l.Update(msg)
	*l = updated
	return cmd
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
	case "d":
		return m, m.deleteSelected()
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
	case "d":
		return m, m.deleteSelected()
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
				m.creating++
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
		m.creating++
		m.app.launch(func() {
			m.app.createParFiles(m.currentDir, item.title, item.size)
		})
	}
	return m.spinCmd()
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
	loadCmd, err := m.buildModelForDirectory(newDir)
	if err != nil {
		return m.reportError("change directory", err)
	}
	// If going to parent, select the directory we came from
	if prevDir != "" {
		idx := findDirIndex(m.fileList.Items(), prevDir)
		if idx >= 0 {
			m.fileList.Select(idx)
		}
	}
	return loadCmd
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
	// contain legacy records stored under source file names. Defensive
	// check: startup cleanup removes such records, so the branch is next
	// to unreachable; the item is left untouched, a log entry is enough
	if !isPar2Name(item.par2Filename) {
		m.app.logger.Warn("refusing to verify a record without .par2 extension",
			slog.String("dir", item.dir),
			slog.String("par2_file", item.par2Filename))
		return nil
	}
	item.status = index.StatusProcessing
	item.desc = formatIndexMeta(item, m.indexListExpanded, m.app.now())
	m.indexList.SetItem(m.indexList.Index(), item)
	m.indexList.CursorDown()

	// Ignored-with-log level: the item is already shown as processing and
	// the verification result overwrites the status anyway
	if err := m.app.store.UpdateStatus(item.dir, item.par2Filename, index.StatusProcessing); err != nil {
		m.app.logStoreError("failed to update index status in database", err)
	}
	m.refreshStats()
	m.verifying++
	m.app.launch(func() {
		m.app.verifyParFiles(item.dir, item)
	})
	return m.spinCmd()
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
		indexItem.desc = formatIndexMeta(indexItem, m.indexListExpanded, m.app.now())
		items[i] = indexItem
	}

	m.setIndexList(items, "All Indexes from Database (Press Esc to return)")
}

// collapseIndexList rebuilds the index list from the .par2 files of the
// current directory — the collapsed mode. Archives that appeared since the
// last build are parsed by the returned background command (plan item 7.2).
func (m *Model) collapseIndexList() tea.Cmd {
	m.indexListExpanded = false
	osFiles, err := os.ReadDir(m.currentDir)
	if err != nil {
		m.reportError("read directory", err)
		return nil
	}
	scan := m.processIndexFiles(osFiles)
	m.setIndexList(scan.items, "Index Files")
	if len(scan.pending) == 0 {
		return nil
	}
	return m.loadArchivedCmd(m.currentDir, scan.pending)
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
	item.desc = formatIndexMeta(item, m.indexListExpanded, m.app.now())
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
	item.desc = formatIndexMeta(item, m.indexListExpanded, m.app.now())
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

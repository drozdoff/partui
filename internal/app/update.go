package app

// Dependencies used in the Update method live on the model's App (m.app):
// - m.app.wg: sync.WaitGroup - for tracking background goroutines (wg.Add(1))
// - m.app.db: *bbolt.DB - database for index storage (index.PutIndex, calculateBottomPanelStats)
// - m.app.logger: *slog.Logger - structured logging
//
// Functions called:
// - m.app.createParFiles() - creates PAR2 files in background
// - m.app.verifyParFiles() - verifies PAR2 files in background
// - m.buildModelForDirectory() - rebuilds model for directory navigation
// - printShowStopper() - exits on fatal error
// - m.app.calculateBottomPanelStats() - calculates bottom panel statistics
// - index.PutIndex() - stores index in database

import (
	"log/slog"
	"os"
	"partui/internal/index"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		m.indexList.Update(msg)
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			if m.focus == FocusFile {
				m.focus = FocusIndex
			} else {
				m.focus = FocusFile
			}
			return m, nil
		case "up", "down", "pgup", "pgdown", "home", "end":
			// Navigate the focused list
			if m.focus == FocusFile {
				m.fileList, cmd = m.fileList.Update(msg)
			} else {
				m.indexList, cmd = m.indexList.Update(msg)
			}
			return m, cmd
		case " ":
			// Only work in file list
			if m.focus != FocusFile {
				return m, nil
			}
			item, ok := selectedFileItem(m)
			if !ok {
				return m, nil
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
		case "c":
			// Only work in file list
			if m.focus != FocusFile {
				return m, nil
			}
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
						addIndex(m, item.title+".par2")
						m.app.wg.Add(1)
						go m.app.createParFiles(m.currentDir, item.title, item.size)
					}
				}
			} else {
				item, ok := selectedFileItem(m)
				if !ok {
					return m, nil
				}
				item.processing = true
				m.fileList.SetItem(m.fileList.Index(), item)
				m.fileList.CursorDown()
				addIndex(m, item.title+".par2")
				m.app.wg.Add(1)
				go m.app.createParFiles(m.currentDir, item.title, item.size)
			}
		case "v":
			// Only work in index list
			if m.focus != FocusIndex {
				return m, nil
			}
			item, ok := selectedIndexChanged(m)
			if !ok {
				return m, nil
			}
			item.status = index.StatusProcessing
			item.desc = formatIndexItemDesc(item, m.indexListExpanded)
			m.indexList.SetItem(m.indexList.Index(), item)
			m.indexList.CursorDown()

			if err := m.app.updateIndexStatusSafe(item.dir, item.par2Filename, index.StatusProcessing); err != nil {
				m.app.logger.Warn("failed to update index status in database", slog.String("error", err.Error()))
			}
			m.bottomPanel = m.app.calculateBottomPanelStats()
			m.app.wg.Add(1)
			go m.app.verifyParFiles(item.dir, item)
		case "i":
			// Expand indexList to show all indexes from database
			if m.indexListExpanded {
				// m.focus = FocusFile
				// TODO: i button toggle, do the same as for Esc
			} else {
				m.indexListExpanded = true
				m.focus = FocusIndex
				indexes, err := m.app.allIndexesSafe()
				if err != nil {
					m.app.logger.Warn("failed to load all indexes", slog.String("error", err.Error()))
					return m, nil
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
			}
		case "esc":
			// Return to local indexes
			if m.indexListExpanded {
				m.indexListExpanded = false
				osFiles, err := os.ReadDir(m.currentDir)
				if err != nil {
					m.app.logger.Warn("failed to read directory", slog.String("dir", m.currentDir), slog.String("error", err.Error()))
					return m, nil
				}
				indexItems := m.processIndexFiles(osFiles)

				m.indexList = list.New(indexItems, newIndexListItemDelegate(m), 0, 0)
				m.indexList.Title = "Index Files"
				m.indexList.SetShowStatusBar(false)
				m.indexList.DisableQuitKeybindings()
			}
		// TODO: repair damaged files
		case "enter":
			// Only work in file list
			if m.focus != FocusFile {
				return m, nil
			}
			item, ok := selectedFileItem(m)
			if !ok {
				return m, nil
			}
			if item.isDir {
				var err error
				dir := m.currentDir + "/" + item.title
				newDir := filepath.Dir(dir)

				// Save current dir to select when going back
				var prevDir string = ""
				if item.title == "../" {
					prevDir = m.currentDir
				}

				err = m.buildModelForDirectory(newDir)
				printShowStopper(m.app.logger, err)
				// If going to parent, select the directory we came from
				if prevDir != "" {
					idx := findDirIndex(m.fileList.Items(), prevDir)
					if idx >= 0 {
						m.fileList.Select(idx)
					}
				}
			}
		}
	case parCreateFinishedMsg:
		title := msg.title
		iIdx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
		if iIdx != -1 {
			item.processing = false
			item.archive = true
			m.fileList.SetItem(iIdx, item)
		}

		// Update indexes database. The record is about the .par2 file,
		// not the source file: msg.title is the source file name
		if err := m.app.putIndexSafe(msg.dir, msg.title+".par2", index.StatusOK, time.Now()); err != nil {
			m.app.logger.Warn("failed to save index to database", slog.String("error", err.Error()))
		}

		// Update indexList - find the entry and mark it as completed
		indexItems := m.indexList.Items()
		iiIndex, ii := findListItem(indexItems, title+".par2", func(ii indexListItem) string { return ii.par2Filename })
		if iiIndex != -1 {
			ii.desc = formatIndexItemDesc(ii, m.indexListExpanded)
			ii.status = index.StatusOK
			ii.lastVerified = time.Now()
			indexItems[iiIndex] = ii
			m.indexList.SetItems(indexItems)
		}

		// Update bottom panel statistics
		m.bottomPanel = m.app.calculateBottomPanelStats()

		return m, cmd
	case parCreateErrorMsg:
		if msg.err != nil {
			m.app.logger.Error("failed to create PAR2 file", slog.String("dir", msg.dir), slog.String("title", msg.title), slog.String("error", msg.err.Error()))
		}

		title := msg.title
		idx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
		if idx != -1 {
			item.processing = false
			m.fileList.SetItem(idx, item)
		}

		// Update indexList - mark entry as damaged
		indexItems := m.indexList.Items()
		iiIndex, ii := findListItem(indexItems, title+".par2", func(ii indexListItem) string { return ii.par2Filename })
		if iiIndex != -1 {
			ii.desc = formatIndexItemDesc(ii, m.indexListExpanded)
			ii.status = index.StatusDamaged
			ii.lastVerified = time.Now()
			indexItems[iiIndex] = ii
			m.indexList.SetItems(indexItems)
		}

		// Update bottom panel statistics
		m.bottomPanel = m.app.calculateBottomPanelStats()

	case indexVerifyFinishedMsg:
		if msg.err != nil {
			m.app.logger.Error("failed to verify PAR2 file", slog.String("dir", msg.dir), slog.String("par2_file", msg.par2Filename), slog.String("error", msg.err.Error()))
		}

		title := msg.par2Filename

		// If directory is missing (StatusMissing), don't update LastVerified
		if msg.status == index.StatusMissing {
			err := m.app.updateIndexStatusSafe(msg.dir, title, msg.status)
			if err != nil {
				m.app.logger.Warn("failed to update index status in database", slog.String("error", err.Error()))
			}
		} else {
			err := m.app.putIndexSafe(msg.dir, title, msg.status, time.Now())
			if err != nil {
				m.app.logger.Warn("failed to save index to database", slog.String("error", err.Error()))
			}
		}

		idx, item := findListItem(m.indexList.Items(), title, func(fi indexListItem) string { return fi.par2Filename })
		if idx != -1 {
			item.status = msg.status
			// Only update lastVerified if not StatusMissing
			if msg.status != index.StatusMissing {
				item.lastVerified = time.Now()
			}
			item.desc = formatIndexItemDesc(item, m.indexListExpanded)
			m.indexList.SetItem(idx, item)
		}

		// Update bottom panel statistics
		m.bottomPanel = m.app.calculateBottomPanelStats()

	case spinner.TickMsg:
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	}

	return m, nil
}

func addIndex(m *Model, par2Filename string) {
	indexItems := m.indexList.Items()
	indexItems = append(indexItems, indexListItem{
		par2Filename: par2Filename,
		desc:         index.StatusDisplay(index.StatusProcessing),
		status:       index.StatusProcessing,
	})
	m.indexList.SetItems(indexItems)
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

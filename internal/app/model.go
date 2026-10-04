package app

import (
	"fmt"
	"log/slog"
	"os"
	"partui/internal/index"
	"partui/internal/par2"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/list"
)

type ListFocus string

const (
	FocusFile  ListFocus = "file"
	FocusIndex ListFocus = "index"
)

// formatFileSize converts bytes to human-readable format
func formatFileSize(bytes uint64) string {
	const kb = 1024
	const mb = 1024 * kb
	const gb = 1024 * mb

	switch {
	case bytes < kb:
		return fmt.Sprintf("%d bytes", bytes)
	case bytes < mb:
		return fmt.Sprintf("%.2f KB", float64(bytes)/kb)
	case bytes < gb:
		return fmt.Sprintf("%.2f MB", float64(bytes)/mb)
	default:
		return fmt.Sprintf("%.2f GB", float64(bytes)/gb)
	}
}

// formatIndexItemDesc formats the description for an index list item
func formatIndexItemDesc(item indexListItem, expanded bool) string {
	status := index.StatusDisplay(item.status)

	if expanded && !item.lastVerified.IsZero() {
		// Expanded mode: YYYY.MM.DD HH:MM | /path/to/dir | status
		return item.lastVerified.Format("2006.01.02 15:04") + " | " + item.dir + " | " + status
	}
	// Normal mode: /path/to/dir | status
	return item.dir + " | " + status
}

type Model struct {
	// app owns the shared dependencies: logger, store, waitgroup, program
	app *App

	fileList    list.Model
	indexList   list.Model
	bottomPanel BottomPanel

	currentDir    string
	filesSelected int
	archived      []string

	focus ListFocus

	// indexListExpanded - when true, indexList shows all indexes from DB
	indexListExpanded bool

	width  int
	height int
}

// NewModel creates a Model bound to the given App
func NewModel(app *App) *Model {
	return &Model{app: app}
}

// buildModelForDirectory rebuilds the model for the given directory
func (m *Model) buildModelForDirectory(dir string) error {
	m.currentDir = dir

	// Default focus on file list
	m.focus = FocusFile

	osFiles, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	items := make([]fileListItem, 0, len(osFiles)+1)
	indexItems := m.processIndexFiles(osFiles)

	// unless root directory, add .. to a list of directories
	parent := filepath.Dir(dir)
	if dir != parent {
		items = append(items, fileListItem{title: "../", isDir: true, desc: "Parent directory"})
	}

	// build a list of files and directories to display
	for _, file := range osFiles {
		name := file.Name()

		fileInfo, err := file.Info()
		if err != nil || hiddenFile(name) {
			continue
		}

		title := name
		if file.IsDir() {
			title = title + "/"
		}

		items = append(items, fileListItem{
			title:   title,
			size:    uint64(fileInfo.Size()),
			archive: slices.Contains(m.archived, name),
			desc:    formatFileSize(uint64(fileInfo.Size())),
			isDir:   file.IsDir(),
		})
	}

	// Sorting: directories first, then files, by name
	slices.SortFunc(items, func(a, b fileListItem) int {
		// If types are different - directories first
		if a.isDir != b.isDir {
			if a.isDir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.title, b.title)
	})

	listItems := make([]list.Item, len(items))
	for i, it := range items {
		listItems[i] = it
	}

	m.fileList = list.New(listItems, newFileListItemDelegate(m), 0, 0)
	m.fileList.Title = m.currentDir
	m.fileList.SetShowHelp(false)
	m.fileList.SetShowStatusBar(false)
	m.fileList.SetFilterState(list.Unfiltered)
	m.fileList.DisableQuitKeybindings()

	// Populate indexList with existing .par2 index files
	m.indexList = list.New(indexItems, newIndexListItemDelegate(m), 0, 0)
	m.indexList.Title = "Index Files"
	m.indexList.SetShowHelp(false)
	m.indexList.SetShowStatusBar(false)
	m.indexList.SetFilterState(list.Unfiltered)
	m.indexList.DisableQuitKeybindings()

	// Initialize bottom panel statistics
	m.bottomPanel = m.app.calculateBottomPanelStats()

	return nil
}

func isParIndex(filename string) bool {
	// PAR2 index files end with ".par2" and do not include a volume suffix like ".vol000+01" before the extension
	lf := strings.ToLower(filename)
	if !strings.HasSuffix(lf, ".par2") {
		return false
	}
	// Detect volume files: name.vol<digits>+<digits>.par2
	var volPattern = regexp.MustCompile(`\.vol\d+\+\d+\.par2$`)
	return !volPattern.MatchString(lf)
}

func hiddenFile(name string) bool {
	// ignore hidden files and .par2 files
	return strings.HasPrefix(name, ".") || filepath.Ext(name) == ".par2"
}

func (m *Model) processIndexFiles(files []os.DirEntry) []list.Item {
	indexItems := make([]indexListItem, 0)
	for _, file := range files {
		name := file.Name()
		if isParIndex(name) {
			archived, err := par2.ListPar2Filenames(m.currentDir + "/" + name)
			if err != nil {
				m.app.logger.Warn("failed to list PAR2 filenames",
					slog.String("dir", m.currentDir),
					slog.String("filename", name),
					slog.String("error", err.Error()))
				continue
			}
			m.archived = append(m.archived, archived...)

			indexItem := indexListItem{
				par2Filename: name,
				dir:          m.currentDir,
				status:       index.StatusOK,
			}

			// Save to database if available
			descriptor, err := m.app.store.Get(m.currentDir, name)
			switch {
			case err == nil && descriptor != nil:
				indexItem.status = descriptor.Status
				indexItem.lastVerified = descriptor.LastVerified
			case err == nil:
				now := m.app.now()
				err = m.app.store.Put(m.currentDir, name, index.StatusOK, now)
				if err != nil {
					m.app.logStoreError("failed to save index to database", err)
				} else {
					indexItem.lastVerified = now
				}
			default:
				m.app.logStoreError("failed to check index in database", err)
			}

			// Format description based on mode
			indexItem.desc = formatIndexItemDesc(indexItem, m.indexListExpanded)

			indexItems = append(indexItems, indexItem)

		}
	}

	// Sort: damaged first, then by lastVerified ascending
	slices.SortFunc(indexItems, func(a, b indexListItem) int {
		// Damaged files first
		aDamaged := a.status == index.StatusDamaged
		bDamaged := b.status == index.StatusDamaged
		if aDamaged != bDamaged {
			if aDamaged {
				return -1
			}
			return 1
		}

		// All others by lastVerified ascending (older first)
		return a.lastVerified.Compare(b.lastVerified)
	})

	items := make([]list.Item, len(indexItems))
	for i, it := range indexItems {
		items[i] = it
	}
	return items
}

package app

import (
	"fmt"
	"os"
	"partui/internal/index"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
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

// formatIndexMeta builds the dim meta part of an index list row (plan
// UI_revamp, step UI-4). The collapsed mode shows only the relative
// verification time — every index of the list lives in the current
// directory, which is already displayed in the header; the expanded mode
// prefixes the directory. A zero LastVerified (a fresh placeholder entry)
// yields no time.
func formatIndexMeta(item indexListItem, expanded bool, now time.Time) string {
	var t string
	if !item.lastVerified.IsZero() {
		t = formatRelativeTime(item.lastVerified, now)
	}
	switch {
	case t == "":
		if expanded {
			return item.dir
		}
		return ""
	case expanded:
		return item.dir + " · " + t
	default:
		return t
	}
}

// formatRelativeTime renders t relative to now in compact English
// ("just now", "5 min ago", "2 h ago", "yesterday", "3 d ago") and falls
// back to the absolute date for anything older than a week. Abbreviated
// units keep the format plural-free. A future t (clock skew) is "just now".
func formatRelativeTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	default:
		return t.Format("02.01.2006")
	}
}

type Model struct {
	// app owns the shared dependencies: logger, store, waitgroup, program
	app *App

	fileList  list.Model
	indexList list.Model
	header    Header

	currentDir    string
	filesSelected int
	archived      []string

	focus ListFocus

	// indexListExpanded - when true, indexList shows all indexes from DB
	indexListExpanded bool

	// confirm is the open delete-confirmation dialog, nil while closed.
	// While open it consumes every key before the lists see any
	confirm *confirmDialog

	// startupCmd carries the background load of the start directory's
	// archives into the running program: the model is built before
	// tea.NewProgram, so Init is the only way to hand the command over
	startupCmd tea.Cmd

	// errText/errUntil hold the error reported by App.reportError; the
	// line is shown in the footer status line until errUntil
	errText  string
	errUntil time.Time

	// verifying/creating count the launched background PAR2 tasks; the
	// footer status line renders the spinner and the worker-slot indicator
	// from them (plan UI_revamp, step UI-5). spinning tracks whether a
	// spinner tick chain is running, so repeated launches do not stack
	// chains.
	verifying int
	creating  int
	spinner   spinner.Model
	spinning  bool

	width  int
	height int
}

// NewModel creates a Model bound to the given App
func NewModel(app *App) *Model {
	return &Model{app: app, spinner: spinner.New(spinner.WithSpinner(spinner.Dot))}
}

// jobsActive reports whether any background PAR2 task is running
func (m *Model) jobsActive() bool {
	return m.verifying > 0 || m.creating > 0
}

// spinCmd starts the footer spinner tick chain; safe to call on every task
// launch — an already running chain is left alone
func (m *Model) spinCmd() tea.Cmd {
	if m.spinning || !m.jobsActive() {
		return nil
	}
	m.spinning = true
	return m.spinner.Tick
}

// taskDone decrements a launched-task counter, flooring at zero: the finish
// handlers may run without a matching launch (tests, a shutdown while the
// task was still queued for a worker slot)
func taskDone(n *int) {
	*n--
	if *n < 0 {
		*n = 0
	}
}

// buildModelForDirectory rebuilds the model for the given directory. On
// failure the model is left untouched, so navigation can simply report the
// error and continue in the current directory. The returned command parses
// the not yet parsed .par2 index files in the background (plan item 7.2);
// it is nil when every archive in the directory is already parsed and fresh.
func (m *Model) buildModelForDirectory(dir string) (tea.Cmd, error) {
	osFiles, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	m.currentDir = dir

	// Default focus on file list
	m.focus = FocusFile

	// Archive marks of the previous directory must not leak here; the marks
	// known from the session cache are restored right below, without any
	// par2 IO
	m.archived = nil

	items := make([]fileListItem, 0, len(osFiles)+1) // +1 for the "../" parent entry
	scan := m.processIndexFiles(osFiles)
	m.archived = scan.archived
	indexItems := scan.items

	// unless root directory, add .. to a list of directories
	parent := filepath.Dir(dir)
	if dir != parent {
		items = append(items, fileListItem{title: "../", isDir: true, desc: "Parent directory"})
	}

	// build a list of files and directories to display
	for _, file := range osFiles {
		name := file.Name()

		fileInfo, err := file.Info()
		if err != nil {
			continue
		}

		// .par2 files live in the index list, not here; dotfiles show up
		// only with show_hidden in the config (plan item 6.1)
		if filepath.Ext(name) == ".par2" || (!m.app.cfg.ShowHidden && strings.HasPrefix(name, ".")) {
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
	m.setIndexList(indexItems, "Index Files")

	// Initialize bottom panel statistics
	m.refreshStats()

	m.applyLayout()

	// The archive marks and the honest statuses of the not yet parsed
	// archives arrive with the background load
	if len(scan.pending) == 0 {
		return nil, nil
	}
	return m.loadArchivedCmd(dir, scan.pending), nil
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

// shortReason compresses a parsing error to what fits into a list item
// description; the full reason is in the log
func shortReason(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	const maxLen = 60
	if runes := []rune(msg); len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return msg
}

// isPar2Name reports whether the name can denote a PAR2 file at all. Only
// such names may be verified and stored in the index database.
func isPar2Name(filename string) bool {
	return strings.HasSuffix(strings.ToLower(filename), ".par2")
}

// indexScan is the outcome of scanning one directory for .par2 index files
type indexScan struct {
	// items are the ready index list entries
	items []list.Item
	// archived holds the data file names of the archives already parsed in
	// this session — enough to set the file list marks without par2 IO
	archived []string
	// pending are the archives not parsed yet; the caller hands them to
	// loadArchivedCmd
	pending []string
}

// processIndexFiles builds the index list entries without any par2 IO
// (plan item 7.2): statuses come from the database and the session cache,
// archives that were not parsed yet go to scan.pending for the background
// load, which also creates their initial database records — unparseable
// files never enter the database.
func (m *Model) processIndexFiles(files []os.DirEntry) indexScan {
	var scan indexScan
	indexItems := make([]indexListItem, 0)
	for _, file := range files {
		name := file.Name()
		if !isParIndex(name) {
			continue
		}

		indexItem := indexListItem{
			par2Filename: name,
			dir:          m.currentDir,
			status:       index.StatusOK,
		}

		entry, cached := m.app.par2Names.get(m.currentDir, name)
		switch {
		case cached && entry.err != "":
			// a known-broken archive: the same honest status as a fresh
			// parse failure, the full reason was logged by the parser
			indexItem.status = index.StatusUnverifiable
			indexItem.desc = "не удалось прочитать: " + shortReason(entry.err)
			indexItems = append(indexItems, indexItem)
			continue
		case cached:
			scan.archived = append(scan.archived, entry.names...)
		default:
			scan.pending = append(scan.pending, name)
		}

		// reflect the stored status if available
		descriptor, err := m.app.store.Get(m.currentDir, name)
		switch {
		case err == nil && descriptor != nil:
			indexItem.status = descriptor.Status
			indexItem.lastVerified = descriptor.LastVerified
		case err == nil && cached:
			// parsed before and not in the database: a plain record write,
			// no par2 IO involved
			now := m.app.now()
			err = m.app.store.Put(m.currentDir, name, index.StatusOK, now)
			if err != nil {
				m.app.logStoreError("failed to save index to database", err)
			} else {
				indexItem.lastVerified = now
			}
		case err == nil:
			// not parsed yet: the record is written by the background load
			// after a successful parse
		default:
			m.app.logStoreError("failed to check index in database", err)
		}

		// Format the meta part based on mode
		indexItem.desc = formatIndexMeta(indexItem, m.indexListExpanded, m.app.now())

		indexItems = append(indexItems, indexItem)
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

	scan.items = make([]list.Item, len(indexItems))
	for i, it := range indexItems {
		scan.items[i] = it
	}
	return scan
}

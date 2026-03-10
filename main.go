package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"partui/index"
	"partui/par2"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	gopar "github.com/akalin/gopar/par2"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.etcd.io/bbolt"
)

type ListFocus string

const (
	FocusFile  ListFocus = "file"
	FocusIndex ListFocus = "index"
)

// BottomPanel displays index statistics at the bottom of the UI
type BottomPanel struct {
	totalCount      int
	processingCount int
	damagedItems    []DamagedIndex
}

// DamagedIndex represents a damaged index file
type DamagedIndex struct {
	par2Filename string
	dir          string
}

type Model struct {
	fileList    list.Model
	indexList   list.Model
	bottomPanel BottomPanel

	currentDir    string
	filesSelected int
	archived      []string

	focus ListFocus

	width  int
	height int
}

// TODO: make percentage configurable
const percentage = 5

// ReverifyThreshold defines how old an index must be to require re-verification on startup
// Default: 30 days (can be changed here or made configurable)
// const ReverifyThreshold = 30 * 24 * time.Hour
const ReverifyThreshold = 5 * 24 * time.Hour

var numProcs = runtime.GOMAXPROCS(-1)

// counting semaphore for any kind of workers
var workers = make(chan struct{}, numProcs)

var wg sync.WaitGroup
var program *tea.Program
var ctx context.Context

var db *bbolt.DB

func (m *Model) Init() tea.Cmd {
	return tea.SetWindowTitle("Partui")
}

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
		case "up", "down", "j", "k":
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
			item := m.fileList.SelectedItem().(fileListItem)
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
			// override this behaviour in config??? specify index file name for a list of files?
			// UI for config?
			// TODO: handle change dir during process. file list is now strictly for current dir.
			if m.filesSelected > 0 {
				for idx, i := range m.fileList.Items() {
					item := i.(fileListItem)
					if item.selected {
						item.processing = true
						item.selected = false
						m.fileList.SetItem(idx, item)
						m.addIndex(item.title + ".par2")
						wg.Add(1)
						go createParFiles(m.currentDir, item.title, item.size)
					}
				}
			} else {
				item := m.fileList.SelectedItem().(fileListItem)
				item.processing = true
				m.fileList.SetItem(m.fileList.Index(), item)
				m.fileList.CursorDown()
				m.addIndex(item.title + ".par2")
				wg.Add(1)
				go createParFiles(m.currentDir, item.title, item.size)
			}
		case "v":
			// Only work in index list
			if m.focus != FocusIndex {
				return m, nil
			}
			item := m.indexList.SelectedItem().(indexListItem)
			item.status = index.StatusProcessing
			m.indexList.SetItem(m.indexList.Index(), item)
			m.indexList.CursorDown()
			index.PutIndex(db, m.currentDir, item.par2Filename, index.StatusProcessing, time.Now())
			m.bottomPanel = calculateBottomPanelStats(db)
			wg.Add(1)
			go verifyParFiles(m.currentDir, item)
		// TODO: repair damaged files
		case "enter":
			// Only work in file list
			if m.focus != FocusFile {
				return m, nil
			}
			item := m.fileList.SelectedItem().(fileListItem)
			if item.isDir {
				var err error
				dir := m.currentDir + "/" + item.title
				newDir := filepath.Dir(dir)

				// Save current dir to select when going back
				var prevDir string = ""
				if item.title == "../" {
					prevDir = m.currentDir
				}

				err = buildModelForDirectory(m, newDir)
				printShowStopper(err)
				// If going to parent, select the directory we came from
				if prevDir != "" {
					idx := findDirIndex(m.fileList.Items(), prevDir)
					if idx > 0 {
						m.fileList.Select(idx)
					}
				}
			}
		case "pgup", "pgdown", "home", "end":
			// TODO: only works in file list. Make it focus window dependent
			m.fileList, cmd = m.fileList.Update(msg)
			return m, cmd
		}
	case parCreateFinishedMsg:
		title := msg.title
		iIdx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
		if iIdx != -1 {
			item.processing = false
			item.archive = true
			m.fileList.SetItem(iIdx, item)
		}

		// Update indexList - find the entry and mark it as completed
		indexItems := m.indexList.Items()
		iiIndex, _ := findListItem(indexItems, title+".par2", func(ii indexListItem) string { return ii.par2Filename })
		if iiIndex != -1 {
			ii := indexItems[iiIndex].(indexListItem)
			index.PutIndex(db, msg.dir, ii.par2Filename, index.StatusOK, time.Now())
			ii.desc = index.StatusDisplay(index.StatusOK)
			ii.status = index.StatusOK
			indexItems[iiIndex] = ii
			m.indexList.SetItems(indexItems)
		}

		// Update bottom panel statistics
		m.bottomPanel = calculateBottomPanelStats(db)

		return m, cmd
	case parCreateErrorMsg:
		if msg.err != nil {
			log.Printf("Warning: failed to verify %s: %v", msg.title, msg.err)
		}

		title := msg.title
		idx, item := findListItem(m.fileList.Items(), title, func(fi fileListItem) string { return fi.title })
		if idx != -1 {
			item.processing = false
			m.fileList.SetItem(idx, item)
		}

		// Update indexList - mark entry as damaged
		indexItems := m.indexList.Items()
		iiIndex, _ := findListItem(indexItems, title+".par2", func(ii indexListItem) string { return ii.par2Filename })
		if iiIndex != -1 {
			ii := indexItems[iiIndex].(indexListItem)
			index.PutIndex(db, msg.dir, ii.par2Filename, index.StatusDamaged, time.Now())
			ii.desc = index.StatusDisplay(index.StatusDamaged)
			ii.status = index.StatusDamaged
			indexItems[iiIndex] = ii
			m.indexList.SetItems(indexItems)
		}

		// Update bottom panel statistics
		m.bottomPanel = calculateBottomPanelStats(db)

	case indexVerifyFinishedMsg:
		if msg.err != nil {
			log.Printf("Warning: failed to verify %s: %v", msg.par2Filename, msg.err)
		}

		title := msg.par2Filename

		err := index.PutIndex(db, msg.dir, title, msg.status, time.Now())
		if err != nil {
			log.Printf("Warning: failed to save index to database: %v", err)
		}

		idx, item := findListItem(m.indexList.Items(), title, func(fi indexListItem) string { return fi.par2Filename })
		if idx != -1 {
			item.status = msg.status
			item.desc = index.StatusDisplay(msg.status)
			m.indexList.SetItem(idx, item)
		}

		// Update bottom panel statistics
		m.bottomPanel = calculateBottomPanelStats(db)

	case spinner.TickMsg:
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	}

	return m, nil
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

func (m *Model) addIndex(par2Filename string) {
	indexItems := m.indexList.Items()
	indexItems = append(indexItems, indexListItem{
		par2Filename: par2Filename,
		desc:         index.StatusDisplay(index.StatusProcessing),
		status:       index.StatusProcessing,
	})
	m.indexList.SetItems(indexItems)
}

// calculateBottomPanelStats computes statistics for the bottom panel
func calculateBottomPanelStats(db *bbolt.DB) BottomPanel {
	if db == nil {
		return BottomPanel{}
	}

	// Get total count
	totalCount, err := index.CountAll(db)
	if err != nil {
		log.Printf("Warning: failed to count indexes: %v", err)
		return BottomPanel{}
	}

	// Get damaged items
	damagedItems, err := index.GetDamaged(db)
	if err != nil {
		log.Printf("Warning: failed to get damaged indexes: %v", err)
		return BottomPanel{}
	}

	// Get all indexes to count processing ones
	allIndexes, err := index.All(db)
	if err != nil {
		log.Printf("Warning: failed to get all indexes: %v", err)
		return BottomPanel{}
	}

	processingCount := 0
	for _, idx := range allIndexes {
		if idx.Status == index.StatusProcessing {
			processingCount++
		}
	}

	// Convert to DamagedIndex format
	damaged := make([]DamagedIndex, len(damagedItems))
	for i, d := range damagedItems {
		damaged[i] = DamagedIndex{
			par2Filename: d.Filename,
			dir:          d.Dir,
		}
	}

	return BottomPanel{
		totalCount:      totalCount,
		processingCount: processingCount,
		damagedItems:    damaged,
	}
}

func createParFiles(dir string, title string, size uint64) {
	workers <- struct{}{}
	defer func() {
		<-workers
		wg.Done()
	}()

	// don't start a new file if program exited
	select {
	case <-ctx.Done():
		return
	default:
		// do nothing
	}

	filePath := dir + "/" + title
	parPath := filePath + ".par2"

	options := gopar.CreateOptions{
		SliceByteCount:  par2.CalculateBlockSize(size, percentage),
		NumParityShards: -1,
		NumGoroutines:   numProcs,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}

	err := gopar.Create(parPath, []string{filePath}, options)
	if err != nil {
		program.Send(parCreateErrorMsg{dir: dir, title: title, err: err})
		return
	}

	program.Send(parCreateFinishedMsg{dir: dir, title: title})
}

func verifyParFiles(dir string, item indexListItem) {
	workers <- struct{}{}
	defer func() {
		<-workers
		wg.Done()
	}()

	// don't start a new file if program exited
	select {
	case <-ctx.Done():
		return
	default:
		// do nothing
	}

	// TODO: propper logging
	parPath := dir + "/" + item.par2Filename
	options := gopar.VerifyOptions{
		NumGoroutines:  numProcs,
		VerifyDelegate: &par2.FilenameCollector{},
	}
	res, err := gopar.Verify(parPath, options)

	msg := indexVerifyFinishedMsg{
		dir:          dir,
		par2Filename: item.par2Filename,
		status:       index.StatusOK,
	}

	if err != nil {
		// TODO: separate status for not verifiable index files
		msg.status = index.StatusDamaged
		msg.err = err
	} else if res.ShardCounts.RepairNeeded() {
		msg.status = index.StatusDamaged
	}

	program.Send(msg)
}

func (m *Model) View() string {
	style := lipgloss.NewStyle()

	w := m.width / 2
	h := m.height
	if h < 0 {
		h = 0
	}
	m.fileList.SetHeight(h)
	m.fileList.SetWidth(w)

	m.indexList.SetHeight(h)
	m.indexList.SetWidth(w)

	// Apply focus styling to borders
	leftStyle := style
	rightStyle := style

	left := leftStyle.Render(m.fileList.View())
	right := rightStyle.Render(m.indexList.View())

	lists := lipgloss.JoinHorizontal(lipgloss.Center, left, right)

	// Add bottom panel
	bottomPanel := m.bottomPanel.View(m.width)

	return lipgloss.JoinVertical(lipgloss.Left, lists, bottomPanel)
}

// View renders the bottom panel
func (bp BottomPanel) View(width int) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#AAAAAA")).
		Background(lipgloss.Color("#1a1a1a")).
		Padding(0, 1)

	// Format statistics line
	statsLine := fmt.Sprintf(
		"Total: %d | Verifying: %d | Damaged: %d",
		bp.totalCount,
		bp.processingCount,
		len(bp.damagedItems),
	)

	// Build damaged files list (truncated if too long)
	damagedLine := ""
	if len(bp.damagedItems) > 0 {
		damagedNames := make([]string, 0, len(bp.damagedItems))
		for _, d := range bp.damagedItems {
			damagedNames = append(damagedNames, d.dir+"/"+d.par2Filename)
		}
		damagedLine = " | Damaged: " + strings.Join(damagedNames, ", ")
	}

	content := statsLine + damagedLine

	// Truncate to fit width
	if width > 3 && len(content) > width {
		content = content[:width-3] + "..."
	}

	return style.Width(width).Render(content)
}

func buildModelForDirectory(m *Model, dir string) error {
	m.currentDir = dir

	// Default focus on file list
	m.focus = FocusFile

	osFiles, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	items := make([]list.Item, 0, len(osFiles)+1)
	indexItems := processIndexFiles(m, osFiles)

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
			desc:    fmt.Sprintf("%d bytes", fileInfo.Size()),
			isDir:   file.IsDir(),
		})
	}

	// Sorting: directories first, then files, by name
	sort.Slice(items, func(i, j int) bool {
		a := items[i].(fileListItem)
		b := items[j].(fileListItem)
		// If types are different - directories first
		if a.isDir != b.isDir {
			return a.isDir
		}
		return a.title < b.title
	})

	m.fileList = list.New(items, newFileListItemDelegate(m), 0, 0)
	m.fileList.Title = m.currentDir
	m.fileList.SetShowHelp(false)
	m.fileList.SetShowStatusBar(false)
	m.fileList.SetFilterState(list.Unfiltered)
	m.fileList.SetShowPagination(true)
	m.fileList.DisableQuitKeybindings()

	// Populate indexList with existing .par2 index files
	m.indexList = list.New(indexItems, newIndexListItemDelegate(m), 0, 0)
	m.indexList.Title = "Index Files"
	m.indexList.SetShowHelp(false)
	m.indexList.SetShowStatusBar(false)
	m.indexList.SetFilterState(list.Unfiltered)
	m.indexList.SetShowPagination(false)
	m.indexList.DisableQuitKeybindings()

	// Initialize bottom panel statistics
	m.bottomPanel = calculateBottomPanelStats(db)

	return nil
}

func findDirIndex(items []list.Item, dirName string) int {
	// dirName is full path, we need to find the directory entry
	// The entry title will be "dirname/"
	targetTitle := filepath.Base(dirName) + "/"
	for i, item := range items {
		fi := item.(fileListItem)
		if fi.isDir && fi.title == targetTitle {
			return i
		}
	}
	return 0
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

func processIndexFiles(m *Model, files []os.DirEntry) []list.Item {
	indexItems := make([]list.Item, 0)
	for _, file := range files {
		name := file.Name()
		if isParIndex(name) {
			archived, err := par2.ListPar2Filenames(m.currentDir + "/" + name)
			if err != nil {
				// TODO: error handling
				continue
			}
			m.archived = append(m.archived, archived...)

			indexItem := indexListItem{
				par2Filename: name,
				desc:         index.StatusDisplay(index.StatusOK),
				status:       index.StatusOK,
			}

			// Save to database if available
			if db != nil {
				descriptor, err := index.GetIndex(db, m.currentDir, name)
				if err != nil {
					log.Printf("Warning: failed to check index in database: %v", err)
				} else if descriptor != nil {
					indexItem.status = descriptor.Status
					indexItem.desc = index.StatusDisplay(descriptor.Status)
				} else {
					err = index.PutIndex(db, m.currentDir, name, index.StatusOK, time.Now())
					if err != nil {
						log.Printf("Warning: failed to save index to database: %v", err)
					}
				}
			}
			indexItems = append(indexItems, indexItem)

		}
	}
	return indexItems
}

func main() {
	name, err := filepath.Abs(os.Args[0])
	printShowStopper(err)

	// Initialize the index database
	db, err = index.OpenDB(index.GetDBPath())
	defer index.CloseDB(db)

	if err != nil {
		log.Printf("Warning: failed to open index database: %v", err)
		db = nil
	}

	m := Model{}

	err = buildModelForDirectory(&m, filepath.Dir(name))
	printShowStopper(err)

	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	program = tea.NewProgram(&m, tea.WithAltScreen(), tea.WithContext(ctx))

	// Reverify old indexes BEFORE starting the UI
	// This runs in the main thread as required
	if db != nil {
		threshold := time.Now().Add(-ReverifyThreshold)
		oldIndexes, err := index.GetOldIndexes(db, threshold)
		if err != nil {
			log.Printf("Warning: failed to get old indexes: %v", err)
		} else if len(oldIndexes) > 0 {
			log.Printf("Found %d indexes requiring re-verification", len(oldIndexes))

			// Start verify workers for old indexes
			for _, idx := range oldIndexes {
				// set old indexes status to processing
				index.PutIndex(db, idx.Dir, idx.Filename, index.StatusProcessing, idx.LastVerified)

				// start verify goroutine
				wg.Add(1)
				go verifyParFiles(idx.Dir, indexListItem{
					par2Filename: idx.Filename,
				})
			}
			m.bottomPanel = calculateBottomPanelStats(db)
		}
	}

	_, err = program.Run()
	cancel()
	wg.Wait()
	printShowStopper(err)
}

func printShowStopper(err error) {
	if err != nil {
		fmt.Printf("Something went wrong: %v", err)
		os.Exit(1)
	}
}

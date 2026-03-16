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
	tea "github.com/charmbracelet/bubbletea"
	"go.etcd.io/bbolt"
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
	size := formatFileSize(item.size)
	status := index.StatusDisplay(item.status)

	if expanded && !item.lastVerified.IsZero() {
		// Expanded mode: YYYY.MM.DD HH:MM | /path/to/dir | size | status
		return item.lastVerified.Format("2006.01.02 15:04") + " | " + item.dir + " | " + size + " | " + status
	}
	// Normal mode: /path/to/dir | size | status
	return item.dir + " | " + size + " | " + status
}

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

	// indexListExpanded - when true, indexList shows all indexes from DB
	indexListExpanded bool

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

// package variables for package main
var wg sync.WaitGroup
var program *tea.Program
var ctx context.Context
var db *bbolt.DB

// counting semaphore for any kind of workers
var workers = make(chan struct{}, numProcs-1)

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

	program.Send(parCreateFinishedMsg{dir: dir, title: title, size: size})
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
		size:         item.size,
	}

	if err != nil {
		msg.status = index.StatusUnverifiable
		msg.err = err
	} else if res.ShardCounts.RepairNeeded() {
		msg.status = index.StatusDamaged
	}

	program.Send(msg)
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
			desc:    formatFileSize(uint64(fileInfo.Size())),
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
	m.fileList.DisableQuitKeybindings()

	// Populate indexList with existing .par2 index files
	m.indexList = list.New(indexItems, newIndexListItemDelegate(m), 0, 0)
	m.indexList.Title = "Index Files"
	m.indexList.SetShowHelp(false)
	m.indexList.SetShowStatusBar(false)
	m.indexList.SetFilterState(list.Unfiltered)
	m.indexList.DisableQuitKeybindings()

	// Initialize bottom panel statistics
	m.bottomPanel = calculateBottomPanelStats(db)

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

			// Get file info for size
			fileInfo, err := file.Info()
			if err != nil {
				log.Printf("Warning: failed to get file info: %v", err)
				continue
			}
			fileSize := uint64(fileInfo.Size())

			indexItem := indexListItem{
				par2Filename: name,
				dir:          m.currentDir,
				status:       index.StatusOK,
				size:         fileSize,
			}

			// Save to database if available
			if db != nil {
				descriptor, err := index.GetIndex(db, m.currentDir, name)
				if err != nil {
					log.Printf("Warning: failed to check index in database: %v", err)
				} else if descriptor != nil {
					indexItem.status = descriptor.Status
					indexItem.lastVerified = descriptor.LastVerified
					indexItem.size = descriptor.Size
				} else {
					now := time.Now()
					err = index.PutIndex(db, m.currentDir, name, index.StatusOK, now, fileSize)
					if err != nil {
						log.Printf("Warning: failed to save index to database: %v", err)
					} else {
						indexItem.lastVerified = now
					}
				}
			}

			// Format description based on mode
			indexItem.desc = formatIndexItemDesc(indexItem, m.indexListExpanded)

			indexItems = append(indexItems, indexItem)

		}
	}

	// Sort: damaged first, then by lastVerified ascending
	sort.Slice(indexItems, func(i, j int) bool {
		a := indexItems[i].(indexListItem)
		b := indexItems[j].(indexListItem)

		// Damaged files first
		if a.status == index.StatusDamaged && b.status != index.StatusDamaged {
			return true
		}
		if b.status == index.StatusDamaged && a.status != index.StatusDamaged {
			return false
		}

		// All others by lastVerified ascending (older first)
		return a.lastVerified.Before(b.lastVerified)
	})

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
				index.PutIndex(db, idx.Dir, idx.Filename, index.StatusProcessing, idx.LastVerified, idx.Size)

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

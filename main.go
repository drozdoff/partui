package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
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
	status := index.StatusDisplay(item.status)

	if expanded && !item.lastVerified.IsZero() {
		// Expanded mode: YYYY.MM.DD HH:MM | /path/to/dir | status
		return item.lastVerified.Format("2006.01.02 15:04") + " | " + item.dir + " | " + status
	}
	// Normal mode: /path/to/dir | status
	return item.dir + " | " + status
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

// logger
var logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

// getLogPath returns the path to the log file
func getLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".partui", "partui.log")
}

// initLogger initializes structured logging to file
func initLogger() {
	logPath := getLogPath()

	// Ensure directory exists
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatalf("failed to create log directory: %v", err)
	}

	// Open log file
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	// Create JSON handler with INFO level
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	logger = slog.New(handler)
}

// calculateBottomPanelStats computes statistics for the bottom panel
func calculateBottomPanelStats(db *bbolt.DB) BottomPanel {
	if db == nil {
		return BottomPanel{}
	}

	// Get total count
	totalCount, err := index.CountAll(db)
	if err != nil {
		logger.Warn("failed to count indexes", slog.String("error", err.Error()))
		return BottomPanel{}
	}

	// Get damaged items
	damagedItems, err := index.GetDamaged(db)
	if err != nil {
		logger.Warn("failed to get damaged indexes", slog.String("error", err.Error()))
		return BottomPanel{}
	}

	// Get all indexes to count processing ones
	allIndexes, err := index.All(db)
	if err != nil {
		logger.Warn("failed to get all indexes", slog.String("error", err.Error()))
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

	logger.Debug("starting PAR2 creation",
		slog.String("dir", dir),
		slog.String("title", title),
		slog.Uint64("size", size))

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
		logger.Error("PAR2 creation failed",
			slog.String("dir", dir),
			slog.String("title", title),
			slog.String("par_path", parPath),
			slog.String("error", err.Error()))
		program.Send(parCreateErrorMsg{dir: dir, title: title, err: err})
		return
	}

	logger.Info("PAR2 creation successful",
		slog.String("dir", dir),
		slog.String("title", title),
		slog.String("par_path", parPath),
		slog.Uint64("size", size))

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

	logger.Debug("starting PAR2 verification",
		slog.String("dir", dir),
		slog.String("par2_file", item.par2Filename))

	// Check if directory exists before attempting verification
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		logger.Warn("directory not found, marking as missing",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename))

		// Missing directory is a common case for offline/removable media and deserve a special status
		msg := indexVerifyFinishedMsg{
			dir:          dir,
			par2Filename: item.par2Filename,
			status:       index.StatusMissing,
		}
		program.Send(msg)
		return
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
		logger.Error("PAR2 verification failed",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename),
			slog.String("error", err.Error()),
			slog.String("status", "unverifiable"))
		msg.status = index.StatusUnverifiable
		msg.err = err
	} else if res.ShardCounts.RepairNeeded() {
		logger.Warn("PAR2 file damaged",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename),
			slog.Bool("repair_needed", true))
		msg.status = index.StatusDamaged
	} else {
		logger.Debug("PAR2 verification successful",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename))
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
				logger.Warn("failed to list PAR2 filenames",
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
			if db != nil {
				descriptor, err := index.GetIndex(db, m.currentDir, name)
				if err != nil {
					logger.Warn("failed to check index in database",
						slog.String("dir", m.currentDir),
						slog.String("filename", name),
						slog.String("error", err.Error()))
				} else if descriptor != nil {
					indexItem.status = descriptor.Status
					indexItem.lastVerified = descriptor.LastVerified
				} else {
					now := time.Now()
					err = index.PutIndex(db, m.currentDir, name, index.StatusOK, now)
					if err != nil {
						logger.Warn("failed to save index to database",
							slog.String("dir", m.currentDir),
							slog.String("filename", name),
							slog.String("error", err.Error()))
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
	initLogger()
	logger.Info("starting PartUI")

	name, err := filepath.Abs(os.Args[0])
	printShowStopper(err)

	// Initialize the index database
	db, err = index.OpenDB(index.GetDBPath())
	defer index.CloseDB(db)

	if err != nil {
		logger.Error("failed to open index database",
			slog.String("db_path", index.GetDBPath()),
			slog.String("error", err.Error()))
		db = nil
	} else {
		logger.Debug("index database opened",
			slog.String("db_path", index.GetDBPath()))
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
			logger.Warn("failed to get old indexes",
				slog.String("error", err.Error()))
		} else if len(oldIndexes) > 0 {
			logger.Info("found indexes requiring re-verification",
				slog.Int("count", len(oldIndexes)),
				slog.Duration("threshold", ReverifyThreshold))

			// Start verify workers for old indexes
			for _, idx := range oldIndexes {
				// set old indexes status to processing
				index.UpdateIndexStatus(db, idx.Dir, idx.Filename, index.StatusProcessing)

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

	logger.Info("PartUI shutting down")
	printShowStopper(err)
}

func printShowStopper(err error) {
	if err != nil {
		logger.Error("fatal error - shutting down",
			slog.String("error", err.Error()))
		fmt.Printf("Something went wrong: %v", err)
		os.Exit(1)
	}
}

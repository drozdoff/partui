package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"partui/internal/index"
	"partui/internal/par2"
	"runtime"
	"sync"
	"time"

	gopar "github.com/akalin/gopar/par2"
	tea "github.com/charmbracelet/bubbletea"
	"go.etcd.io/bbolt"
)

// ErrStoreUnavailable is returned by index store operations when the
// database could not be opened and the app runs in degraded mode
var ErrStoreUnavailable = errors.New("index store unavailable")

// App owns all application-level dependencies and state that used to live in
// package variables: the logger, the index database, the cancellation context,
// the worker semaphore and the running tea.Program. There is one App per
// process; both main and Model hold a pointer to it.
type App struct {
	logger *slog.Logger
	db     *bbolt.DB

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// workers is a counting semaphore for any kind of workers
	workers chan struct{}

	numProcs int

	// program is assigned right after tea.NewProgram and before any
	// goroutine is started, so no extra synchronization is needed
	// (happens-before via goroutine start)
	program *tea.Program
}

// NewApp creates an App with default settings
func NewApp(logger *slog.Logger) *App {
	numProcs := runtime.GOMAXPROCS(-1)
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		workers:  make(chan struct{}, numProcs-1),
		numProcs: numProcs,
	}
}

// TODO: make percentage configurable
const percentage = 5

// Shutdown cancels the app context, waits for background goroutines and only
// then closes the index database — strictly in that order, so no goroutine
// can write into a closed database.
func (a *App) Shutdown() {
	a.cancel()
	a.wg.Wait()
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			a.logger.Warn("failed to close index database", slog.String("error", err.Error()))
		}
		a.db = nil
	}
	a.logger.Info("PartUI shutting down")
}

// putIndexSafe stores an index in the database. In degraded mode (the db
// failed to open) it is a no-op returning ErrStoreUnavailable.
func (a *App) putIndexSafe(dir, filename string, status index.Status, verifiedAt time.Time) error {
	if a.db == nil {
		return ErrStoreUnavailable
	}
	return index.PutIndex(a.db, dir, filename, status, verifiedAt)
}

// updateIndexStatusSafe updates the status of an existing index, preserving
// LastVerified. In degraded mode it is a no-op returning ErrStoreUnavailable.
func (a *App) updateIndexStatusSafe(dir, filename string, status index.Status) error {
	if a.db == nil {
		return ErrStoreUnavailable
	}
	return index.UpdateIndexStatus(a.db, dir, filename, status)
}

// allIndexesSafe returns all indexes from the database. In degraded mode it
// returns ErrStoreUnavailable.
func (a *App) allIndexesSafe() ([]index.IndexDescriptor, error) {
	if a.db == nil {
		return nil, ErrStoreUnavailable
	}
	return index.All(a.db)
}

// calculateBottomPanelStats computes statistics for the bottom panel.
// A failed query (or degraded mode) is reported via BottomPanel.storeErr
// instead of silently showing zeros.
func (a *App) calculateBottomPanelStats() BottomPanel {
	if a.db == nil {
		return BottomPanel{storeErr: ErrStoreUnavailable}
	}

	// Get total count
	totalCount, err := index.CountAll(a.db)
	if err != nil {
		a.logger.Warn("failed to count indexes", slog.String("error", err.Error()))
		return BottomPanel{storeErr: err}
	}

	// Get damaged items
	damagedItems, err := index.GetDamaged(a.db)
	if err != nil {
		a.logger.Warn("failed to get damaged indexes", slog.String("error", err.Error()))
		return BottomPanel{storeErr: err}
	}

	// Get all indexes to count processing ones
	allIndexes, err := index.All(a.db)
	if err != nil {
		a.logger.Warn("failed to get all indexes", slog.String("error", err.Error()))
		return BottomPanel{storeErr: err}
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

func (a *App) createParFiles(dir string, title string, size uint64) {
	a.workers <- struct{}{}
	defer func() {
		<-a.workers
		a.wg.Done()
	}()

	// don't start a new file if program exited
	select {
	case <-a.ctx.Done():
		return
	default:
		// do nothing
	}

	a.logger.Debug("starting PAR2 creation",
		slog.String("dir", dir),
		slog.String("title", title),
		slog.Uint64("size", size))

	filePath := dir + "/" + title
	parPath := filePath + ".par2"

	options := gopar.CreateOptions{
		SliceByteCount:  par2.CalculateBlockSize(size, percentage),
		NumParityShards: -1,
		NumGoroutines:   a.numProcs,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}

	err := gopar.Create(parPath, []string{filePath}, options)
	if err != nil {
		a.logger.Error("PAR2 creation failed",
			slog.String("dir", dir),
			slog.String("title", title),
			slog.String("par_path", parPath),
			slog.String("error", err.Error()))
		a.program.Send(parCreateErrorMsg{dir: dir, title: title, err: err})
		return
	}

	a.logger.Info("PAR2 creation successful",
		slog.String("dir", dir),
		slog.String("title", title),
		slog.String("par_path", parPath),
		slog.Uint64("size", size))

	a.program.Send(parCreateFinishedMsg{dir: dir, title: title, size: size})
}

func (a *App) verifyParFiles(dir string, item indexListItem) {
	a.workers <- struct{}{}
	defer func() {
		<-a.workers
		a.wg.Done()
	}()

	// don't start a new file if program exited
	select {
	case <-a.ctx.Done():
		return
	default:
		// do nothing
	}

	a.logger.Debug("starting PAR2 verification",
		slog.String("dir", dir),
		slog.String("par2_file", item.par2Filename))

	// Check if directory exists before attempting verification
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		a.logger.Warn("directory not found, marking as missing",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename))

		// Missing directory is a common case for offline/removable media and deserve a special status
		msg := indexVerifyFinishedMsg{
			dir:          dir,
			par2Filename: item.par2Filename,
			status:       index.StatusMissing,
		}
		a.program.Send(msg)
		return
	}

	// TODO: propper logging
	parPath := dir + "/" + item.par2Filename
	options := gopar.VerifyOptions{
		NumGoroutines:  a.numProcs,
		VerifyDelegate: &par2.FilenameCollector{},
	}
	res, err := gopar.Verify(parPath, options)

	msg := indexVerifyFinishedMsg{
		dir:          dir,
		par2Filename: item.par2Filename,
		status:       index.StatusOK,
	}

	if err != nil {
		a.logger.Error("PAR2 verification failed",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename),
			slog.String("error", err.Error()),
			slog.String("status", "unverifiable"))
		msg.status = index.StatusUnverifiable
		msg.err = err
	} else if res.ShardCounts.RepairNeeded() {
		a.logger.Warn("PAR2 file damaged",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename),
			slog.Bool("repair_needed", true))
		msg.status = index.StatusDamaged
	} else {
		a.logger.Debug("PAR2 verification successful",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename))
	}

	a.program.Send(msg)
}

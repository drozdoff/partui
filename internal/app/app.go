package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"partui/internal/config"
	"partui/internal/index"
	"partui/internal/par2"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// App owns all application-level dependencies and state that used to live in
// package variables: the configuration, the logger, the index store, the
// PAR2 runner, the clock, the cancellation context, the worker semaphore and
// the running tea.Program. There is one App per process; both main and Model
// hold a pointer to it.
type App struct {
	cfg    config.Config
	logger *slog.Logger
	store  IndexStore
	par2   Par2Runner
	clock  func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// workers is a counting semaphore for any kind of workers;
	// the capacity is cfg.MaxWorkers
	workers chan struct{}

	// par2Names caches the parsed contents of .par2 index files for the
	// session, so navigation does not repeat the par2 IO (plan item 7.2)
	par2Names *par2NameCache

	// program is assigned right after tea.NewProgram and before any
	// goroutine is started, so no extra synchronization is needed
	// (happens-before via goroutine start)
	program *tea.Program
}

// NewApp creates an App with the given configuration: a degraded (noop)
// store — run() replaces it with a real bbolt store on success — the
// production PAR2 runner and the wall clock.
func NewApp(cfg config.Config, logger *slog.Logger) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		cfg:       cfg,
		logger:    logger,
		store:     newNoopStore(logger),
		par2:      defaultRunner{},
		clock:     time.Now,
		ctx:       ctx,
		cancel:    cancel,
		workers:   make(chan struct{}, cfg.MaxWorkers),
		par2Names: newPar2NameCache(),
	}
}

// now returns the injected or wall-clock time
func (a *App) now() time.Time {
	if a.clock == nil {
		return time.Now()
	}
	return a.clock()
}

// launch runs fn in a background goroutine: it tracks the WaitGroup,
// limits concurrency with the workers semaphore and respects context
// cancellation both before and while waiting for a free slot.
func (a *App) launch(fn func()) {
	a.wg.Go(func() {
		select {
		case a.workers <- struct{}{}:
			defer func() { <-a.workers }()
		case <-a.ctx.Done():
			return
		}
		if a.isCancelled() {
			return
		}
		fn()
	})
}

// isCancelled reports whether the app context has been cancelled
func (a *App) isCancelled() bool {
	select {
	case <-a.ctx.Done():
		return true
	default:
		return false
	}
}

// Shutdown cancels the app context, waits for background goroutines and only
// then closes the index store — strictly in that order, so no goroutine
// can write into a closed database.
func (a *App) Shutdown() {
	a.cancel()
	a.wg.Wait()
	if err := a.store.Close(); err != nil && !errors.Is(err, ErrStoreUnavailable) {
		a.logger.Warn("failed to close index store", slog.String("error", err.Error()))
	}
	a.store = newNoopStore(a.logger)
	a.logger.Info("PartUI shutting down")
}

// logStoreError logs a store failure; the degraded-mode sentinel is not
// logged here — noopStore announces it once on its own
func (a *App) logStoreError(msg string, err error) {
	if errors.Is(err, ErrStoreUnavailable) {
		return
	}
	a.logger.Warn(msg, slog.String("error", err.Error()))
}

// createParFiles builds a PAR2 archive for a single file in a worker
// goroutine. It always reports the outcome to the UI — either
// parCreateFinishedMsg or parCreateErrorMsg — so a file marked as
// processing never stays in that state (plan item 4.1); the only exceptions
// are a shutdown while the task is still queued for a worker slot and a
// cancelled context — in both cases the shutdown is not an honest create
// outcome, so the task exits silently (plans/streaming_file.md §6.6.2,
// symmetric with verifyParFiles).
func (a *App) createParFiles(dir string, title string, size uint64) {
	a.logger.Debug("starting PAR2 creation",
		slog.String("dir", dir),
		slog.String("title", title),
		slog.Uint64("size", size))

	// The streaming create is frugal, but hand the freed heap back to the
	// OS right after the task anyway, so parallel large-file tasks do not
	// stack their RSS peaks
	defer debug.FreeOSMemory()

	filePath := filepath.Join(dir, title)
	parPath := filePath + ".par2"

	options := par2.CreateOptions{
		SliceByteCount:  par2.CalculateBlockSize(size, uint64(a.cfg.Percentage)),
		NumParityShards: -1,
		NumGoroutines:   runtime.GOMAXPROCS(0),
		Delegate:        createProgressLog{logger: a.logger, par2File: title + ".par2"},
	}

	err := a.par2.Create(a.ctx, parPath, []string{filePath}, options)

	// A cancellation means the app is shutting down: reporting the create as
	// failed would be a lie, so the task exits silently, symmetric with the
	// queued-task case of launch() and with verifyParFiles.
	if errors.Is(err, context.Canceled) {
		a.logger.Info("PAR2 creation cancelled",
			slog.String("dir", dir),
			slog.String("title", title))
		return
	}

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

// verifyParFiles verifies a single PAR2 file in a worker goroutine and
// reports the outcome as indexVerifyFinishedMsg — OK, Damaged,
// Unverifiable or Missing — so an index set to processing by verifySelected
// always reaches a final status (plan item 4.1). The exceptions are a
// shutdown while the task is queued and a cancelled context: in both cases
// the shutdown is not an honest verification result, so no message is sent
// and the index keeps its processing status (plans/streaming_file.md §5.4).
func (a *App) verifyParFiles(dir string, item indexListItem) {
	a.logger.Debug("starting PAR2 verification",
		slog.String("dir", dir),
		slog.String("par2_file", item.par2Filename))

	// The streaming verify itself is frugal, but hand the freed heap back
	// to the OS right after the task anyway (gopar-period hygiene).
	defer debug.FreeOSMemory()

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

	parPath := filepath.Join(dir, item.par2Filename)
	options := par2.VerifyOptions{
		Delegate: verifyProgressLog{logger: a.logger, par2File: item.par2Filename},
	}
	verifyRes, err := a.par2.Verify(a.ctx, parPath, options)

	// A cancellation means the app is shutting down: reporting the set as
	// Unverifiable would be a lie, so the task exits silently, symmetric
	// with the queued-task case of launch().
	if errors.Is(err, context.Canceled) {
		a.logger.Info("PAR2 verification cancelled",
			slog.String("dir", dir),
			slog.String("par2_file", item.par2Filename))
		return
	}

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
	} else if verifyRes.RepairNeeded {
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

package app

import (
	"fmt"
	"log/slog"
	"os"
	"partui/internal/config"
	"partui/internal/index"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Run initializes logging and runs the application. It reports an error only
// for fatal startup problems; the error is logged here as well, the caller
// (cmd/partui) just turns it into a non-zero exit code.
func Run() error {
	logger := initLogger()
	if err := run(logger); err != nil {
		logger.Error("fatal error", slog.String("error", err.Error()))
		fmt.Printf("Something went wrong: %v", err)
		return err
	}
	return nil
}

// run wires up the application, starts the UI and blocks until it finishes.
// It is the only place allowed to terminate the app on error (via main).
func run(logger *slog.Logger) error {
	logger.Info("starting PartUI")

	name, err := filepath.Abs(os.Args[0])
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	app := NewApp(config.Defaults(), logger)
	// Shutdown cancels the context, waits for goroutines and closes the
	// store — strictly in that order
	defer app.Shutdown()

	// Initialize the index store; on failure the app keeps running in
	// degraded mode (noopStore) without the index memory. Reportable level:
	// the degradation is visible in the UI as "DB: недоступна"
	store, err := newBboltStore(index.GetDBPath())
	if err != nil {
		logger.Error("failed to open index database",
			slog.String("db_path", index.GetDBPath()),
			slog.String("error", err.Error()))
	} else {
		logger.Debug("index database opened",
			slog.String("db_path", index.GetDBPath()))
		app.store = store

		// Remove records stored under source file names by the
		// pre-refactoring binary; they can never be verified
		if removed, err := cleanupNonPar2Records(app.store); err != nil {
			logger.Warn("failed to clean up index records",
				slog.String("error", err.Error()))
		} else if len(removed) > 0 {
			files := make([]string, 0, len(removed))
			for _, d := range removed {
				files = append(files, filepath.Join(d.Dir, d.Filename))
			}
			logger.Info("removed index records without .par2 extension",
				slog.Int("count", len(removed)),
				slog.String("files", strings.Join(files, ", ")))
		}
	}

	m := NewModel(app)

	if err := m.buildModelForDirectory(filepath.Dir(name)); err != nil {
		return fmt.Errorf("build model for start dir %s: %w", filepath.Dir(name), err)
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(app.ctx))
	app.program = p

	// Reverify old indexes BEFORE starting the UI
	// This runs in the main thread as required
	reverifyAfter := time.Duration(app.cfg.ReverifyDays) * 24 * time.Hour
	threshold := app.now().Add(-reverifyAfter)
	oldIndexes, err := app.store.Old(threshold)
	if err != nil {
		app.logStoreError("failed to get old indexes", err)
	} else if len(oldIndexes) > 0 {
		logger.Info("found indexes requiring re-verification",
			slog.Int("count", len(oldIndexes)),
			slog.Duration("threshold", reverifyAfter))

		// Start verify workers for old indexes
		for _, idx := range oldIndexes {
			// set old indexes status to processing; on failure the stale
			// status stays until the verification overwrites it with the
			// real result, so a log entry is enough
			if err := app.store.UpdateStatus(idx.Dir, idx.Filename, index.StatusProcessing); err != nil {
				app.logStoreError("failed to mark index as processing", err)
			}

			// start verify goroutine
			app.launch(func() {
				app.verifyParFiles(idx.Dir, indexListItem{
					par2Filename: idx.Filename,
				})
			})
		}
		m.refreshStats()
	}

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run UI: %w", err)
	}

	return nil
}

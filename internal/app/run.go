package app

import (
	"fmt"
	"log/slog"
	"os"
	"partui/internal/index"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ReverifyThreshold defines how old an index must be to require re-verification on startup
// Default: 30 days (can be changed here or made configurable)
// const ReverifyThreshold = 30 * 24 * time.Hour
const ReverifyThreshold = 5 * 24 * time.Hour

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

	app := NewApp(logger)
	// Shutdown cancels the context, waits for goroutines and closes the db
	// — strictly in that order
	defer app.Shutdown()

	// Initialize the index database; on failure the app keeps running in
	// degraded mode without the index memory
	db, err := index.OpenDB(index.GetDBPath())
	if err != nil {
		logger.Error("failed to open index database",
			slog.String("db_path", index.GetDBPath()),
			slog.String("error", err.Error()))
	} else {
		logger.Debug("index database opened",
			slog.String("db_path", index.GetDBPath()))
		app.db = db
	}

	m := NewModel(app)

	if err := m.buildModelForDirectory(filepath.Dir(name)); err != nil {
		return fmt.Errorf("build model for start dir %s: %w", filepath.Dir(name), err)
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(app.ctx))
	app.program = p

	// Reverify old indexes BEFORE starting the UI
	// This runs in the main thread as required
	if app.db != nil {
		threshold := time.Now().Add(-ReverifyThreshold)
		oldIndexes, err := index.GetOldIndexes(app.db, threshold)
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
				index.UpdateIndexStatus(app.db, idx.Dir, idx.Filename, index.StatusProcessing)

				// start verify goroutine
				app.wg.Add(1)
				go app.verifyParFiles(idx.Dir, indexListItem{
					par2Filename: idx.Filename,
				})
			}
			m.bottomPanel = app.calculateBottomPanelStats()
		}
	}

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run UI: %w", err)
	}

	return nil
}

func printShowStopper(logger *slog.Logger, err error) {
	if err != nil {
		logger.Error("fatal error - shutting down",
			slog.String("error", err.Error()))
		fmt.Printf("Something went wrong: %v", err)
		os.Exit(1)
	}
}

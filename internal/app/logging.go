package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"partui/internal/config"
)

// initLogger initializes structured logging to cfg.LogFile with the level
// from cfg.LogLevel. Failure to create the log file is fatal for the app
// (plan item 4.1): the error is returned before the UI starts and Run turns
// it into a non-zero exit code.
func initLogger(cfg config.Config) (*slog.Logger, error) {
	level, err := cfg.SlogLevel()
	if err != nil {
		return nil, err
	}

	logPath, err := config.ExpandHome(cfg.LogFile)
	if err != nil {
		return nil, err
	}

	// Ensure directory exists
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}

	// Open log file
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", logPath, err)
	}

	// Create JSON handler with the configured level
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: level,
	})

	return slog.New(handler), nil
}

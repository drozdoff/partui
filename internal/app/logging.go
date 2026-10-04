package app

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
)

// getLogPath returns the path to the log file
func getLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".partui", "partui.log")
}

// initLogger initializes structured logging to file
func initLogger() *slog.Logger {
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

	return slog.New(handler)
}

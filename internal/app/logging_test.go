package app

import (
	"os"
	"path/filepath"
	"testing"

	"partui/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Item 6.1: the log level comes from the config, not from the hardcoded
// INFO — a debug record only lands in the file on log_level: debug.
func TestInitLogger_Levels(t *testing.T) {
	tests := []struct {
		level     string
		wantDebug bool
	}{
		{"debug", true},
		{"info", false},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			// the parent directory does not exist yet — initLogger creates it
			logPath := filepath.Join(t.TempDir(), "logs", "partui.log")

			logger, err := initLogger(config.Config{LogFile: logPath, LogLevel: tt.level})
			require.NoError(t, err)

			logger.Debug("debug record")
			logger.Info("info record")

			data, err := os.ReadFile(logPath)
			require.NoError(t, err)
			assert.Contains(t, string(data), `"msg":"info record"`)
			if tt.wantDebug {
				assert.Contains(t, string(data), `"msg":"debug record"`)
			} else {
				assert.NotContains(t, string(data), `"msg":"debug record"`)
			}
		})
	}
}

// Defaults point the log at ~/.partui/partui.log; a leading "~" must expand
// to the home directory, not become a literal "~" directory.
func TestInitLogger_ExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	logger, err := initLogger(config.Defaults())
	require.NoError(t, err)
	logger.Info("hello")

	data, err := os.ReadFile(filepath.Join(home, ".partui", "partui.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
}

func TestInitLogger_Errors(t *testing.T) {
	t.Run("unknown level", func(t *testing.T) {
		_, err := initLogger(config.Config{
			LogFile:  filepath.Join(t.TempDir(), "partui.log"),
			LogLevel: "loud",
		})
		assert.ErrorContains(t, err, "log_level")
	})

	t.Run("unwritable path", func(t *testing.T) {
		// a regular file where a directory is needed
		blocker := filepath.Join(t.TempDir(), "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

		_, err := initLogger(config.Config{
			LogFile:  filepath.Join(blocker, "nested", "partui.log"),
			LogLevel: "info",
		})
		assert.ErrorContains(t, err, "log directory")
	})
}

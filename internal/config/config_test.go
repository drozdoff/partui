package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	assert.Equal(t, 5, cfg.Percentage)
	// at least one worker even on a single-core machine: GOMAXPROCS-1
	// would produce an unbuffered semaphore
	assert.Equal(t, max(1, runtime.GOMAXPROCS(0)-1), cfg.MaxWorkers)
	assert.Equal(t, 5, cfg.ReverifyDays)
	assert.False(t, cfg.ShowHidden)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "~/.partui/partui.log", cfg.LogFile)
	assert.NoError(t, cfg.Validate())
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"defaults are valid", func(*Config) {}, ""},
		{"percentage bounds hold", func(*Config) {}, ""},
		{"log level is case-insensitive", func(c *Config) { c.LogLevel = "DEBUG" }, ""},
		{"percentage too small", func(c *Config) { c.Percentage = 0 }, "percentage"},
		{"percentage too large", func(c *Config) { c.Percentage = 101 }, "percentage"},
		{"max_workers below one", func(c *Config) { c.MaxWorkers = 0 }, "max_workers"},
		{"reverify_days below one", func(c *Config) { c.ReverifyDays = 0 }, "reverify_days"},
		{"unknown log level", func(c *Config) { c.LogLevel = "verbose" }, "log_level"},
		{"empty log file", func(c *Config) { c.LogFile = "" }, "log_file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestSlogLevel(t *testing.T) {
	cfg := Defaults()
	cfg.LogLevel = "Warn"
	level, err := cfg.SlogLevel()
	require.NoError(t, err)
	assert.Equal(t, "WARN", level.String())
}

// writeDefaultConfig fills $HOME/.partui/config.yml and points HOME at a
// fresh temp dir; it returns the home path.
func writeDefaultConfig(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if content == "" {
		return home
	}
	dir := filepath.Join(home, ".partui")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(content), 0o600))
	return home
}

func TestLoad_NoFile_YieldsDefaults(t *testing.T) {
	home := writeDefaultConfig(t, "")

	cfg, err := Load("")
	require.NoError(t, err)

	want := Defaults()
	want.LogFile = filepath.Join(home, ".partui", "partui.log")
	assert.Equal(t, want, cfg)
}

func TestLoad_File_OverridesDefaults(t *testing.T) {
	home := writeDefaultConfig(t, `
percentage: 42
max_workers: 3
reverify_days: 30
show_hidden: true
log_level: debug
log_file: ~/logs/partui.log
`)

	cfg, err := Load("")
	require.NoError(t, err)

	assert.Equal(t, 42, cfg.Percentage)
	assert.Equal(t, 3, cfg.MaxWorkers)
	assert.Equal(t, 30, cfg.ReverifyDays)
	assert.True(t, cfg.ShowHidden)
	assert.Equal(t, "debug", cfg.LogLevel)
	// a leading "~" in log_file expands to the home directory
	assert.Equal(t, filepath.Join(home, "logs", "partui.log"), cfg.LogFile)
}

// Values missing from the file keep their defaults
func TestLoad_File_PartialKeepsDefaults(t *testing.T) {
	writeDefaultConfig(t, "percentage: 17\n")

	cfg, err := Load("")
	require.NoError(t, err)

	assert.Equal(t, 17, cfg.Percentage)
	assert.Equal(t, 5, cfg.ReverifyDays)
	assert.Equal(t, "info", cfg.LogLevel)
}

// An empty file is not an error and changes nothing
func TestLoad_EmptyFile_YieldsDefaults(t *testing.T) {
	home := writeDefaultConfig(t, "\n")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".partui", "partui.log"), cfg.LogFile)
}

// An explicitly given path must exist
func TestLoad_ExplicitPath_MissingIsError(t *testing.T) {
	writeDefaultConfig(t, "")
	missing := filepath.Join(t.TempDir(), "nope.yml")

	_, err := Load(missing)
	assert.ErrorContains(t, err, "nope.yml")
}

// A path via $PARTUI_CONFIG must exist, too
func TestLoad_EnvPath_MissingIsError(t *testing.T) {
	writeDefaultConfig(t, "")
	t.Setenv(EnvConfigPath, filepath.Join(t.TempDir(), "missing.yml"))

	_, err := Load("")
	assert.ErrorContains(t, err, "missing.yml")
}

// $PARTUI_CONFIG pointing at an existing file wins over the default location
func TestLoad_EnvPath_WinsOverDefault(t *testing.T) {
	home := writeDefaultConfig(t, "percentage: 42\n")
	other := filepath.Join(t.TempDir(), "other.yml")
	require.NoError(t, os.WriteFile(other, []byte("percentage: 7\n"), 0o600))
	t.Setenv(EnvConfigPath, other)

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Percentage)
	assert.Equal(t, filepath.Join(home, ".partui", "partui.log"), cfg.LogFile)
}

// An explicit path argument wins over $PARTUI_CONFIG
func TestLoad_ExplicitPath_WinsOverEnv(t *testing.T) {
	writeDefaultConfig(t, "percentage: 42\n")
	explicit := filepath.Join(t.TempDir(), "explicit.yml")
	require.NoError(t, os.WriteFile(explicit, []byte("percentage: 9\n"), 0o600))
	t.Setenv(EnvConfigPath, filepath.Join(t.TempDir(), "env.yml"))

	cfg, err := Load(explicit)
	require.NoError(t, err)
	assert.Equal(t, 9, cfg.Percentage)
}

// PARTUI_* variables override both defaults and file values
func TestLoad_EnvOverridesFile(t *testing.T) {
	writeDefaultConfig(t, "percentage: 42\nlog_level: debug\n")
	t.Setenv("PARTUI_PERCENTAGE", "77")
	t.Setenv("PARTUI_SHOW_HIDDEN", "true")
	t.Setenv("PARTUI_LOG_LEVEL", "warn")

	cfg, err := Load("")
	require.NoError(t, err)

	assert.Equal(t, 77, cfg.Percentage)
	assert.True(t, cfg.ShowHidden)
	assert.Equal(t, "warn", cfg.LogLevel)
}

func TestLoad_EnvOnly(t *testing.T) {
	home := writeDefaultConfig(t, "")
	t.Setenv("PARTUI_REVERIFY_DAYS", "12")
	t.Setenv("PARTUI_LOG_FILE", "~/custom.log")

	cfg, err := Load("")
	require.NoError(t, err)

	assert.Equal(t, 12, cfg.ReverifyDays)
	assert.Equal(t, filepath.Join(home, "custom.log"), cfg.LogFile)
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		env     map[string]string
		wantErr string
	}{
		{"invalid yaml", "percentage: [1,\n", nil, "parse config file"},
		{"unknown key", "percentege: 10\n", nil, "parse config file"},
		{"percentage out of range", "percentage: 0\n", nil, "percentage"},
		{"max_workers below one", "max_workers: -3\n", nil, "max_workers"},
		{"unknown log level", "log_level: loud\n", nil, "log_level"},
		{"non-numeric env", "", map[string]string{"PARTUI_PERCENTAGE": "abc"}, "PARTUI_PERCENTAGE"},
		{"non-boolean env", "", map[string]string{"PARTUI_SHOW_HIDDEN": "maybe"}, "PARTUI_SHOW_HIDDEN"},
		{"invalid env value", "", map[string]string{"PARTUI_MAX_WORKERS": "0"}, "max_workers"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeDefaultConfig(t, tt.config)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := Load("")
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

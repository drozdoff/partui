// Package config holds the runtime configuration of PartUI: the PAR2
// redundancy, worker limits, re-verification threshold, hidden-file
// visibility and logging.
//
// Values are assembled by Load: package defaults, then an optional YAML
// file ($PARTUI_CONFIG or ~/.partui/config.yml), then PARTUI_* environment
// variables — each source overriding the previous one.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvConfigPath points at the config file. Unlike the default location the
// file it names must exist.
const EnvConfigPath = "PARTUI_CONFIG"

// Config carries the tunable parameters that used to be magic numbers
// scattered over the app package.
type Config struct {
	// Percentage is the PAR2 redundancy percentage, 1..100
	Percentage int `yaml:"percentage"`

	// MaxWorkers limits the number of parallel PAR2 operations
	MaxWorkers int `yaml:"max_workers"`

	// ReverifyDays is the age (in days) after which an index is
	// re-verified on startup
	ReverifyDays int `yaml:"reverify_days"`

	// ShowHidden makes dotfiles visible in the file list (.par2 files
	// are listed separately in the index list either way)
	ShowHidden bool `yaml:"show_hidden"`

	// LogLevel is one of debug|info|warn|error (case-insensitive)
	LogLevel string `yaml:"log_level"`

	// LogFile is the log file path; a leading "~" expands to the home
	// directory
	LogFile string `yaml:"log_file"`
}

// Defaults returns the configuration that reproduces the previous hardcoded
// behaviour: 5% redundancy, one worker per CPU (but at least one), a
// five-day re-verification threshold, hidden dotfiles and INFO logging to
// ~/.partui/partui.log.
func Defaults() Config {
	return Config{
		Percentage: 5,
		MaxWorkers: max(1, runtime.GOMAXPROCS(0)-1),
		// five days matches the previous code; the old comment claiming
		// 30 days was stale
		ReverifyDays: 5,
		ShowHidden:   false,
		LogLevel:     "info",
		LogFile:      "~/.partui/partui.log",
	}
}

// Load assembles the configuration: defaults, then the YAML file, then the
// PARTUI_* environment variables. An empty path selects $PARTUI_CONFIG and
// falls back to ~/.partui/config.yml. A path given here or via
// $PARTUI_CONFIG must exist, while a missing default file is not an error —
// defaults (plus environment) apply. Invalid YAML, unknown keys and values
// failing Validate are errors.
func Load(path string) (Config, error) {
	cfg := Defaults()

	path, explicit, err := resolvePath(path)
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if explicit || !os.IsNotExist(err) {
			return cfg, fmt.Errorf("read config file %s: %w", path, err)
		}
		// no config file in the default location — defaults are fine
	} else if err := unmarshalStrict(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config file %s: %w", path, err)
	}

	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	if cfg.LogFile, err = ExpandHome(cfg.LogFile); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// resolvePath picks the config file location: an explicitly given path
// wins, then $PARTUI_CONFIG, then the default ~/.partui/config.yml. The
// first two are "explicit" — a missing file there is an error, a missing
// default file just means defaults.
func resolvePath(path string) (resolved string, explicit bool, err error) {
	if path == "" {
		path = os.Getenv(EnvConfigPath)
	}
	if path != "" {
		resolved, err := ExpandHome(path)
		if err != nil {
			return "", false, err
		}
		return resolved, true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".partui", "config.yml"), false, nil
}

// ExpandHome replaces a leading "~/" or a bare "~" with the user's home
// directory; anything else passes through unchanged.
func ExpandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for %s: %w", path, err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

// applyEnv overrides file values with PARTUI_* environment variables that
// are set. Only the variables named below are recognised.
func applyEnv(cfg *Config) error {
	if v, ok, err := envInt("PARTUI_PERCENTAGE"); err != nil {
		return err
	} else if ok {
		cfg.Percentage = v
	}
	if v, ok, err := envInt("PARTUI_MAX_WORKERS"); err != nil {
		return err
	} else if ok {
		cfg.MaxWorkers = v
	}
	if v, ok, err := envInt("PARTUI_REVERIFY_DAYS"); err != nil {
		return err
	} else if ok {
		cfg.ReverifyDays = v
	}
	if v, ok := os.LookupEnv("PARTUI_SHOW_HIDDEN"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", "PARTUI_SHOW_HIDDEN", v, err)
		}
		cfg.ShowHidden = b
	}
	if v, ok := os.LookupEnv("PARTUI_LOG_LEVEL"); ok {
		cfg.LogLevel = v
	}
	if v, ok := os.LookupEnv("PARTUI_LOG_FILE"); ok {
		cfg.LogFile = v
	}
	return nil
}

// envInt reads an integer variable; a variable that is not set yields
// (0, false, nil), a set but non-numeric value is an error.
func envInt(name string) (int, bool, error) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("%s=%q: %w", name, v, err)
	}
	return n, true, nil
}

// unmarshalStrict decodes YAML and rejects unknown keys — a typo like
// "percentege" must not silently fall back to the default value. An empty
// file is not an error.
func unmarshalStrict(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// SlogLevel converts the configured log level into a slog level.
func (c Config) SlogLevel() (slog.Level, error) {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log_level %q: want debug, info, warn or error", c.LogLevel)
	}
}

// Validate reports configuration values that cannot be used as-is. It is
// called by Load and keeps Defaults() honest in tests.
func (c Config) Validate() error {
	if c.Percentage < 1 || c.Percentage > 100 {
		return fmt.Errorf("percentage %d: want 1..100", c.Percentage)
	}
	if c.MaxWorkers < 1 {
		return fmt.Errorf("max_workers %d: want >= 1", c.MaxWorkers)
	}
	if c.ReverifyDays < 1 {
		return fmt.Errorf("reverify_days %d: want >= 1", c.ReverifyDays)
	}
	if _, err := c.SlogLevel(); err != nil {
		return err
	}
	if c.LogFile == "" {
		return fmt.Errorf("log_file: want a non-empty path")
	}
	return nil
}

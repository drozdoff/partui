// Package config holds the runtime configuration of PartUI.
//
// This phase of the refactoring only introduces the structure with defaults
// matching the previously hardcoded values; loading from a file, environment
// and command-line flags arrives with the configuration work (plan item 6).
package config

import "runtime"

// Config carries the tunable parameters that used to be magic numbers
// scattered over the app package.
type Config struct {
	// Percentage is the PAR2 redundancy percentage, 1..100
	Percentage int

	// MaxWorkers limits the number of parallel PAR2 operations
	MaxWorkers int

	// ReverifyDays is the age (in days) after which an index is
	// re-verified on startup
	ReverifyDays int
}

// Defaults returns the configuration that reproduces the previous hardcoded
// behaviour: 5% redundancy, one worker per CPU (but at least one) and a
// five-day re-verification threshold.
func Defaults() Config {
	return Config{
		Percentage: 5,
		MaxWorkers: max(1, runtime.GOMAXPROCS(0)-1),
		// five days matches the previous code; the old comment claiming
		// 30 days was stale
		ReverifyDays: 5,
	}
}

package config

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	assert.GreaterOrEqual(t, cfg.Percentage, 1, "percentage must be usable as-is")
	assert.LessOrEqual(t, cfg.Percentage, 100)
	assert.GreaterOrEqual(t, cfg.MaxWorkers, 1, "MaxWorkers=0 would deadlock the worker semaphore")
	// at least one worker even on a single-core machine: GOMAXPROCS-1
	// would produce an unbuffered semaphore
	assert.Equal(t, max(1, runtime.GOMAXPROCS(0)-1), cfg.MaxWorkers)
	assert.GreaterOrEqual(t, cfg.ReverifyDays, 1)
}

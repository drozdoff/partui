package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogger returns a logger writing JSON records into buf
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func countRetryWarnings(buf *bytes.Buffer) int {
	return strings.Count(buf.String(), "operation failed, retrying")
}

// Item 4.2: a transiently failing store write succeeds on a later attempt,
// and every failed attempt leaves exactly one warning in the log
func TestRetry_SucceedsAfterTransientFailures(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)

	attempts := 0
	err := retry(context.Background(), logger, "put index", 3, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("timeout")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
	assert.Equal(t, 2, countRetryWarnings(&buf))
}

// Item 4.2: exhausting the attempts wraps the last error with the operation
// name; the final failure is not followed by a retry warning — the caller
// reports it
func TestRetry_ExhaustsAttempts(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)

	err := retry(context.Background(), logger, "update index status", 2, func() error {
		return errors.New("boom")
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "update index status: 2 attempts")
	assert.ErrorContains(t, err, "boom")
	assert.Equal(t, 1, countRetryWarnings(&buf))
}

// Item 4.2: cancellation during the backoff wait interrupts the retry, so
// Shutdown is never delayed by a retrying goroutine
func TestRetry_ContextCancelledInterruptsWait(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	err := retry(ctx, logger, "put index", 5, func() error {
		calls++
		cancel() // cancel while the first backoff wait is pending
		return errors.New("boom")
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}

// An already cancelled context does not run the operation at all
func TestRetry_SkipsWhenContextAlreadyCancelled(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := retry(ctx, logger, "put index", 3, func() error {
		calls++
		return nil
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls)
	assert.Zero(t, countRetryWarnings(&buf))
}

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// storeWriteAttempts is how many times a fast index-store write is attempted
// before it is reported as failed
const storeWriteAttempts = 3

// retry repeats fn up to maxAttempts times with a linear delay
// (attempt*100ms) and logs a warning for every failed attempt. The wait
// between attempts is interrupted by ctx cancellation, so a retry never
// delays the application shutdown (plan item 4.2).
//
// It is deliberately applied only to fast index-store writes (bboltStore
// Put and UpdateStatus): bbolt serializes writers, so a transient file-lock
// timeout from a second process is the typical failure, and replaying a
// single-record write is cheap and safe. Deliberately NOT retried:
//   - gopar Create/Verify — minute-scale operations; on real data corruption
//     a rerun is pointless and risks partial artifacts;
//   - os.ReadDir and PAR2 header parsing — deterministic errors, a retry
//     cannot help;
//   - index-store reads (Get/All/Old) — a failing read transaction almost
//     always indicates a more serious problem than a transient hiccup.
func retry(ctx context.Context, logger *slog.Logger, op string, maxAttempts int, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		// the last attempt is reported by the caller, there is nothing
		// left to retry
		if attempt == maxAttempts {
			break
		}

		logger.Warn("operation failed, retrying",
			slog.String("op", op),
			slog.Int("attempt", attempt),
			slog.String("error", lastErr.Error()))

		select {
		case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("%s: %d attempts: %w", op, maxAttempts, lastErr)
}

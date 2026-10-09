package app

import (
	"context"
	"log/slog"

	"partui/internal/par2"
)

// Par2Runner is the interface over PAR2 create/verify/repair operations. It
// is defined here, at the consumer side, so tests can substitute a fake
// instead of running the real implementation. The interface carries only
// partui types and contexts — gopar is gone from the runtime entirely
// (stage 2 of plans/streaming_file.md); it stays a test-only oracle for
// internal/par2.
type Par2Runner interface {
	Create(ctx context.Context, parPath string, files []string, opts par2.CreateOptions) error
	Verify(ctx context.Context, parPath string, opts par2.VerifyOptions) (par2.VerifyResult, error)
	Repair(ctx context.Context, parPath string, opts par2.RepairOptions) (par2.RepairResult, error)
}

// defaultRunner is the production runner: all operations are the streaming
// implementations from internal/par2 (memory O(sliceByteCount), ctx-aware).
type defaultRunner struct{}

func (defaultRunner) Create(ctx context.Context, parPath string, files []string, opts par2.CreateOptions) error {
	return par2.CreateStream(ctx, parPath, files, opts)
}

func (defaultRunner) Verify(ctx context.Context, parPath string, opts par2.VerifyOptions) (par2.VerifyResult, error) {
	results, err := par2.VerifyStream(ctx, parPath, opts.Delegate)
	if err != nil {
		return par2.VerifyResult{}, err
	}
	for _, r := range results {
		if r.RepairNeeded {
			return par2.VerifyResult{RepairNeeded: true}, nil
		}
	}
	return par2.VerifyResult{}, nil
}

func (defaultRunner) Repair(ctx context.Context, parPath string, opts par2.RepairOptions) (par2.RepairResult, error) {
	return par2.RepairStream(ctx, parPath, opts)
}

// verifyProgressLog routes verify progress to the Debug log; progress
// rendering in the UI is a separate task (plans/streaming_file.md §5.5).
type verifyProgressLog struct {
	logger   *slog.Logger
	par2File string
}

func (l verifyProgressLog) OnProgress(done, total int) {
	l.logger.Debug("PAR2 verification progress",
		slog.String("par2_file", l.par2File),
		slog.Int("done", done),
		slog.Int("total", total))
}

// createProgressLog is verifyProgressLog's twin: create progress goes to
// the Debug log as well (plans/streaming_file.md §6.4).
type createProgressLog struct {
	logger   *slog.Logger
	par2File string
}

func (l createProgressLog) OnProgress(done, total int) {
	l.logger.Debug("PAR2 creation progress",
		slog.String("par2_file", l.par2File),
		slog.Int("done", done),
		slog.Int("total", total))
}

// repairProgressLog is the repair twin of the two above: both repair phases
// (the verify-style scan and the file reassembly) go to the Debug log
// (plans/repair.md §4).
type repairProgressLog struct {
	logger   *slog.Logger
	par2File string
}

func (l repairProgressLog) OnScanProgress(done, total int) {
	l.logger.Debug("PAR2 repair scan progress",
		slog.String("par2_file", l.par2File),
		slog.Int("done", done),
		slog.Int("total", total))
}

func (l repairProgressLog) OnWriteProgress(done, total int) {
	l.logger.Debug("PAR2 repair write progress",
		slog.String("par2_file", l.par2File),
		slog.Int("done", done),
		slog.Int("total", total))
}

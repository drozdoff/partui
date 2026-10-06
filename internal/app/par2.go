package app

import (
	"context"
	"log/slog"

	"partui/internal/par2"

	gopar "github.com/akalin/gopar/par2"
)

// Par2Runner is the interface over PAR2 create/verify operations. It is
// defined here, at the consumer side, so tests can substitute a fake instead
// of running the real implementation. The interface carries only partui
// types — gopar stays an implementation detail of the runner (stage 1 of
// plans/streaming_file.md).
type Par2Runner interface {
	Create(parPath string, files []string, opts par2.CreateOptions) error
	Verify(ctx context.Context, parPath string, opts par2.VerifyOptions) (par2.VerifyResult, error)
}

// defaultRunner is the production runner. Verify is the streaming
// implementation from internal/par2 (memory O(sliceByteCount), ctx-aware);
// create still delegates to gopar until the streaming create of stage 2
// replaces it.
type defaultRunner struct{}

func (defaultRunner) Create(parPath string, files []string, opts par2.CreateOptions) error {
	return gopar.Create(parPath, files, gopar.CreateOptions{
		SliceByteCount:  opts.SliceByteCount,
		NumParityShards: opts.NumParityShards,
		NumGoroutines:   opts.NumGoroutines,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	})
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

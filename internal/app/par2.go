package app

import (
	gopar "github.com/akalin/gopar/par2"
)

// Par2Runner is the interface over PAR2 create/verify operations. It is
// defined here, at the consumer side, so tests can substitute a fake instead
// of running the real gopar.
type Par2Runner interface {
	Create(parPath string, files []string, opts gopar.CreateOptions) error
	Verify(parPath string, opts gopar.VerifyOptions) (gopar.VerifyResult, error)
}

// goparRunner is a thin wrapper over the static gopar functions
type goparRunner struct{}

func (goparRunner) Create(parPath string, files []string, opts gopar.CreateOptions) error {
	return gopar.Create(parPath, files, opts)
}

func (goparRunner) Verify(parPath string, opts gopar.VerifyOptions) (gopar.VerifyResult, error) {
	return gopar.Verify(parPath, opts)
}

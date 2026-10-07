// Vendored from github.com/akalin/gopar (package rsec16, coder.go),
// revision a776223763d3 (v0.0.0-20210524065929), as part of the streaming
// PAR2 implementation (plans/streaming_file.md §6). Original code:
//
//	Copyright (c) 2017, Frederick Akalin
//	All rights reserved.
//
//	Redistribution and use in source and binary forms, with or without
//	modification, are permitted provided that the following conditions are met:
//	    * Redistributions of source code must retain the above copyright
//	      notice, this list of conditions and the following disclaimer.
//	    * Redistributions in binary form must reproduce the above copyright
//	      notice, this list of conditions and the following disclaimer in the
//	      documentation and/or other materials provided with the distribution.
//	    * Neither the name of the copyright holder nor the
//	      names of its contributors may be used to endorse or promote products
//	      derived from this software without specific prior written permission.
//
//	THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
//	AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
//	IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
//	ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
//	LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
//	CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
//	SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
//	INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
//	CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
//	ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
//	POSSIBILITY OF SUCH DAMAGE.
//
// The full license text also lives in the LICENSE file next to this package.
//
// Adaptations against the original:
//   - only the PAR2 Vandermonde half of coder.go is kept: the Cauchy coder
//     and ReconstructData belong to repair, which stays in gopar;
//   - the Coder type is replaced by ParityMatrix below: the streaming create
//     never holds all data shards at once, so instead of the whole-matrix
//     GenerateParity it needs the incremental Accumulate (accumulate.go);
//   - constructor errors replace gopar's panics — an empty data file reaches
//     the constructor with zero data shards, and a panic in a worker
//     goroutine would take the whole app down (plans/streaming_file.md
//     §6.6.3);
//   - NewPAR2Vandermonde is the old NewCoderPAR2Vandermonde with the same
//     validation and the same matrix construction;
//   - DefaultNumGoroutines falls back to GOMAXPROCS when cpuid cannot detect
//     the physical core count (it reports 0 on, e.g., darwin/arm64) — the
//     upstream value would fail the constructor's goroutine-count check.

package rsec16

import (
	"errors"
	"runtime"

	"partui/internal/par2/gf2p16"

	"github.com/klauspost/cpuid/v2"
)

// DefaultNumGoroutines returns a default value for the numGoroutines
// parameter to pass into NewPAR2Vandermonde. This is not necessarily
// GOMAXPROCS.
func DefaultNumGoroutines() int {
	numGoroutines := runtime.GOMAXPROCS(0)
	physicalCores := cpuid.CPU.PhysicalCores
	// Use the number of physical cores as the default instead of
	// the number of virtual cores, which GOMAXPROCS
	// is. Hyperthreading doesn't actually help our workload, and
	// indeed it hurts it a bit.
	if physicalCores < numGoroutines {
		numGoroutines = physicalCores
	}
	if numGoroutines < 1 {
		// cpuid reports 0 physical cores where it cannot detect them
		// (e.g. darwin/arm64); GOMAXPROCS is the only sane fallback.
		numGoroutines = runtime.GOMAXPROCS(0)
	}
	return numGoroutines
}

var generators []gf2p16.T

func init() {
	// TODO: Generate this table at compile time.
	for i := 0; i < (1 << 16); i++ {
		if i%3 == 0 || i%5 == 0 || i%17 == 0 || i%257 == 0 {
			continue
		}
		g := gf2p16.T(2).Pow(uint32(i))
		generators = append(generators, g)
	}
}

// ParityMatrix is the parity part of the PAR2 encoding matrix: row j holds
// the coefficients that turn the data shards into the j-th parity shard.
// The rows are exactly the Vandermonde rows of the PAR2 spec — the same
// construction newCoderPAR2Vandermonde uses, so the parity bytes this
// matrix produces are readable by par2cmdline and gopar alike.
type ParityMatrix struct {
	matrix        gf2p16.Matrix
	numGoroutines int
}

// NewPAR2Vandermonde builds the parity matrix for the given shard counts,
// mirroring rsec16.NewCoderPAR2Vandermonde: element (i, j) of the matrix is
// generators[j]^i, where generators are the elements of order 65535 picked
// by the PAR2 spec construction.
func NewPAR2Vandermonde(dataShards, parityShards, numGoroutines int) (ParityMatrix, error) {
	if dataShards <= 0 {
		return ParityMatrix{}, errors.New("invalid data shard count")
	}
	if parityShards <= 0 {
		return ParityMatrix{}, errors.New("invalid parity shard count")
	}
	if numGoroutines <= 0 {
		return ParityMatrix{}, errors.New("invalid goroutine count")
	}

	if dataShards > len(generators) {
		return ParityMatrix{}, errors.New("too many data shards")
	}

	if parityShards > (1<<16)-1 {
		return ParityMatrix{}, errors.New("too many parity shards")
	}

	// The Vandermonde rows of the PAR2 encoding matrix (see the comment on
	// the upstream NewCoderPAR2Vandermonde for the full layout): column j is
	// powered by the row index i.
	matrix := newVandermondeMatrix(parityShards, dataShards, func(i int) gf2p16.T {
		return generators[i]
	})
	return ParityMatrix{matrix: matrix, numGoroutines: numGoroutines}, nil
}

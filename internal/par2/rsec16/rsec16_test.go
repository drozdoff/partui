package rsec16

// The vendored matrix and the new Accumulate must be indistinguishable from
// gopar's coder (plans/streaming_file.md §6.7): the coefficients
// element-wise (TestParityMatrix_EqGoparCoder) and the accumulated parity
// against GenerateParity (TestAccumulate_EqGenerateParity).

import (
	"math/rand"
	"testing"

	"partui/internal/par2/gf2p16"

	gopargf2p16 "github.com/akalin/gopar/gf2p16"
	goparrsec16 "github.com/akalin/gopar/rsec16"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// par2Generators rebuilds the generator list from gopar's own field tables:
// the ith smallest element of order 65535, i.e. 2^i skipping the i divisible
// by 3, 5, 17 or 257 (the PAR2 spec construction of gopar's coder.go).
func par2Generators(count int) []gopargf2p16.T {
	generators := make([]gopargf2p16.T, 0, count)
	for i := 0; len(generators) < count; i++ {
		if i%3 == 0 || i%5 == 0 || i%17 == 0 || i%257 == 0 {
			continue
		}
		generators = append(generators, gopargf2p16.T(2).Pow(uint32(i)))
	}
	return generators
}

// TestParityMatrix_EqGoparCoder pins the matrix coefficients element-wise:
// ours against a Vandermonde rebuilt with gopar's own gf2p16 tables.
func TestParityMatrix_EqGoparCoder(t *testing.T) {
	for _, tc := range []struct{ dataShards, parityShards int }{
		{1, 1},
		{1, 3},
		{2, 3},
		{5, 3},
		{17, 7},
		{33, 1},
	} {
		t.Run("", func(t *testing.T) {
			pm, err := NewPAR2Vandermonde(tc.dataShards, tc.parityShards, 1)
			require.NoError(t, err)

			generators := par2Generators(tc.dataShards)
			want := gopargf2p16.NewMatrixFromFunction(tc.parityShards, tc.dataShards, func(i, j int) gopargf2p16.T {
				return generators[j].Pow(uint32(i))
			})
			for i := 0; i < tc.parityShards; i++ {
				for j := 0; j < tc.dataShards; j++ {
					// compare as uint16: two distinct gf2p16.T types with
					// equal values are not DeepEqual
					assert.Equal(t, uint16(want.At(i, j)), uint16(pm.matrix.At(i, j)),
						"coefficient (%d,%d) differs from gopar's", i, j)
				}
			}
		})
	}
}

// TestParityMatrix_Errors pins the constructor's error paths — gopar panics
// in these cases, CreateStream needs errors (the empty-data-file fix).
func TestParityMatrix_Errors(t *testing.T) {
	_, err := NewPAR2Vandermonde(0, 3, 1)
	assert.ErrorContains(t, err, "invalid data shard count")

	_, err = NewPAR2Vandermonde(4, 0, 1)
	assert.ErrorContains(t, err, "invalid parity shard count")

	_, err = NewPAR2Vandermonde(4, 3, 0)
	assert.ErrorContains(t, err, "invalid goroutine count")

	_, err = NewPAR2Vandermonde(len(generators)+1, 3, 1)
	assert.ErrorContains(t, err, "too many data shards")

	_, err = NewPAR2Vandermonde(4, 1<<16, 1)
	assert.ErrorContains(t, err, "too many parity shards")
}

// TestAccumulate_EqGenerateParity is the property test of §6.4: accumulating
// slice by slice must produce exactly GenerateParity's parity shards.
func TestAccumulate_EqGenerateParity(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for _, tc := range []struct {
		shards, length, numGoroutines int
	}{
		{1, 4, 1},
		{1, 4096, 1},
		{2, 4, 1},
		{3, 100, 1},
		{5, 1024, 1},
		{17, 4096, 1},
		// the parallel accumulation must land on the same bytes
		{5, 1024, 4},
		{33, 4096, 8},
		{2, 16, 8}, // more goroutines than the length allows: splits down to 1
	} {
		t.Run("", func(t *testing.T) {
			shards := make([][]byte, tc.shards)
			for i := range shards {
				shards[i] = make([]byte, tc.length)
				rng.Read(shards[i])
			}

			pm, err := NewPAR2Vandermonde(tc.shards, 3, tc.numGoroutines)
			require.NoError(t, err)
			parity := make([][]byte, 3)
			for i := range parity {
				parity[i] = make([]byte, tc.length)
			}
			for col, shard := range shards {
				pm.Accumulate(parity, col, shard)
			}

			coder, err := goparrsec16.NewCoderPAR2Vandermonde(tc.shards, 3, 1)
			require.NoError(t, err)
			assert.Equal(t, coder.GenerateParity(shards), parity,
				"accumulated parity must equal GenerateParity")
		})
	}
}

// TestDefaultNumGoroutines pins the adaptation: the default must never be
// zero, even where cpuid cannot detect physical cores (darwin/arm64).
func TestDefaultNumGoroutines(t *testing.T) {
	assert.GreaterOrEqual(t, DefaultNumGoroutines(), 1)
}

// TestAccumulateMatchesVendoredField sanity-checks the two gf2p16 copies
// against each other: the vendored multiply and gopar's must agree on the
// same operands.
func TestAccumulateMatchesVendoredField(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 1000; i++ {
		a := gf2p16.T(rng.Uint64())
		b := gf2p16.T(rng.Uint64())
		assert.Equal(t,
			gopargf2p16.T(a).Times(gopargf2p16.T(b)),
			gopargf2p16.T(a.Times(b)))
	}
}

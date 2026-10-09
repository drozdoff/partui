package rsec16

// The reconstruction half of the PAR2 coder (plans/repair.md): the decode
// side of the streaming repair. gopar's ReconstructData (rsec16/coder.go)
// builds a U×dataShards reconstruction matrix and multiplies whole shards —
// repair cannot afford either, so only the U×U inverse of the parity
// submatrix is built here, and the shards flow through AccumulateSelected
// one slice at a time (the mirror image of the create-side Accumulate).

import (
	"errors"
	"sync"

	"partui/internal/par2/gf2p16"
)

// At returns the element (i, j) of the parity matrix: row i is the parity
// shard written with exponent i, column j is the data slice with the global
// index j (the recovery-set order of the main packet).
func (pm ParityMatrix) At(i, j int) gf2p16.T {
	return pm.matrix.At(i, j)
}

// AccumulateSelected is Accumulate restricted to the given parity rows:
//
//	out[k][p] ^= matrix.At(rows[k], col) ⊗ src[p]
//
// for k in [0, len(rows)) and every byte offset p — the syndrome pass of
// the repair, where only the rows whose parity shards are actually used
// take contributions. The buffers must be zeroed before the first call
// (or hold the parity shard they accumulate onto) and keep the same length
// as src throughout.
func (pm ParityMatrix) AccumulateSelected(rows []int, out [][]byte, col int, src []byte) {
	if len(rows) != len(out) {
		panic("mismatched row and buffer counts")
	}
	if len(out) == 0 {
		return
	}
	dataLength := len(src)
	perGoroutineDataLength, numGoroutines := calculateParallelParams(dataLength, pm.numGoroutines, 16, 16)
	if numGoroutines < 2 {
		accumulateSelectedSlice(pm.matrix, rows, out, col, src, 0, dataLength)
		return
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := range numGoroutines {
		go func(i int) {
			defer wg.Done()
			start := i * perGoroutineDataLength
			end := min(start+perGoroutineDataLength, dataLength)
			accumulateSelectedSlice(pm.matrix, rows, out, col, src, start, end)
		}(i)
	}
	wg.Wait()
}

// accumulateSelectedSlice multiplies the src range [dataStart, dataEnd) by
// the coefficient of every selected row and XORs the result into the same
// byte range of the matching out buffer.
func accumulateSelectedSlice(m gf2p16.Matrix, rows []int, out [][]byte, col int, src []byte, dataStart, dataEnd int) {
	for k, row := range rows {
		c := m.At(row, col)
		gf2p16.MulAndAddByteSliceLE(c, src[dataStart:dataEnd], out[k][dataStart:dataEnd])
	}
}

// ReconstructionMatrix returns the inverse of the parity submatrix P[K][M]:
// rows are the used parity shards (their exponents), columns are the missing
// data slices (their global indices). Multiplying the syndromes by the
// result recovers the missing slices; plans/repair.md §1 has the derivation.
// A singular submatrix is the known flaw of the PAR2 Vandermonde construction
// — the same failure gopar's ReconstructData reports.
func (pm ParityMatrix) ReconstructionMatrix(missingRows, usedParityRows []int) (gf2p16.Matrix, error) {
	if len(missingRows) == 0 {
		return gf2p16.Matrix{}, errors.New("no missing rows")
	}
	if len(missingRows) != len(usedParityRows) {
		return gf2p16.Matrix{}, errors.New("missing and parity row counts differ")
	}

	m := gf2p16.NewMatrixFromFunction(len(usedParityRows), len(missingRows), func(i, j int) gf2p16.T {
		return pm.matrix.At(usedParityRows[i], missingRows[j])
	})
	return m.Inverse()
}

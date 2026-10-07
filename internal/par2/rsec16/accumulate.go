package rsec16

// Accumulate is the streaming-create counterpart of rsec16's
// Coder.GenerateParity (plans/streaming_file.md §6.2): parity is linear over
// GF(2^16), so instead of multiplying the whole matrix by all data shards at
// once, the contribution of one slice at a time can be XOR-accumulated while
// the file streams past. Only one slice and the parity buffers stay in
// memory, regardless of the file size.
//
// The byte-range split across goroutines and the per-slice multiply kernel
// are adapted from gopar's rsec16/matrix.go (applyMatrixParallelData,
// applyMatrixSlice, calculateParallelParams), revision
// a776223763d3 — BSD licensed, see the LICENSE file in this directory —
// so the accumulation costs the same as a GenerateParity call.

import (
	"sync"

	"partui/internal/par2/gf2p16"
)

// Accumulate adds the contribution of the data slice src — the matrix
// column col — to the parity buffers:
//
//	parity[j][p] ^= matrix.At(j, col) ⊗ src[p]
//
// for every parity row j and byte offset p. The buffers must be zeroed
// before the first call and keep the same length as src throughout; the
// parity bytes then equal Coder.GenerateParity over the accumulated slices
// (pinned by TestAccumulate_EqGenerateParity).
//
// The byte range of src is split across numGoroutines goroutines the same
// way applyMatrixParallelData splits it, so the parallelism matches gopar's
// whole-matrix pass; small slices stay single-goroutine.
func (pm ParityMatrix) Accumulate(parity [][]byte, col int, src []byte) {
	if len(parity) == 0 {
		panic("no parity shards")
	}
	dataLength := len(src)
	perGoroutineDataLength, numGoroutines := calculateParallelParams(dataLength, pm.numGoroutines, 16, 16)
	if numGoroutines < 2 {
		accumulateSlice(pm.matrix, col, src, parity, 0, dataLength)
		return
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			defer wg.Done()
			start := i * perGoroutineDataLength
			end := start + perGoroutineDataLength
			if end > dataLength {
				end = dataLength
			}
			accumulateSlice(pm.matrix, col, src, parity, start, end)
		}(i)
	}
	wg.Wait()
}

// accumulateSlice multiplies the src range [dataStart, dataEnd) by every
// parity row's coefficient and XORs the result into the same byte range of
// the parity buffers. Adapted from applyMatrixSlice: gopar assigns the
// first input column (MulByteSliceLE) and adds the rest (MulAndAdd), while
// here every row accumulates into an existing buffer, so all rows are
// multiply-and-add.
func accumulateSlice(m gf2p16.Matrix, col int, src []byte, parity [][]byte, dataStart, dataEnd int) {
	for i, out := range parity {
		c := m.At(i, col)
		gf2p16.MulAndAddByteSliceLE(c, src[dataStart:dataEnd], out[dataStart:dataEnd])
	}
}

// calculateParallelParams is vendored from gopar's rsec16/matrix.go: it
// rounds the per-goroutine length up to at least minPerGoroutineLength and
// to a multiple of perGoroutineLengthDivisor, then reports how many
// goroutines that actually yields.
func calculateParallelParams(totalLength, numGoroutines, minPerGoroutineLength, perGoroutineLengthDivisor int) (perGoroutineLength, newNumGoroutines int) {
	perGoroutineLength = (totalLength + numGoroutines - 1) / numGoroutines
	if perGoroutineLength < minPerGoroutineLength {
		perGoroutineLength = minPerGoroutineLength
	}

	rem := perGoroutineLength % perGoroutineLengthDivisor
	if rem != 0 {
		perGoroutineLength += (perGoroutineLengthDivisor - rem)
	}

	newNumGoroutines = (totalLength + perGoroutineLength - 1) / perGoroutineLength
	return perGoroutineLength, newNumGoroutines
}

package par2

import (
	"math"
)

// blockCountLimits caps the number of PAR2 blocks depending on the file
// size. The values are an empirical compromise between recovery cost and
// granularity and mirror the behaviour of par2cmdline for small files.
// The boundaries are the exact decimal values of the previous float
// literals (1e6, 4e6, 20e6) — changing them changes behaviour, which the
// boundary tests in decoder_test.go must pin down first.
var blockCountLimits = []struct {
	maxSize   uint64 // the row applies while the file size is strictly less
	maxBlocks uint64
}{
	{1_000_000, 7},
	{4_000_000, 15},
	{20_000_000, 31},
	{math.MaxUint64, 127},
}

// blockCountMax returns the maximum number of PAR2 blocks for a file size
func blockCountMax(fSize uint64) uint64 {
	for _, l := range blockCountLimits {
		if fSize < l.maxSize {
			return l.maxBlocks
		}
	}
	return 127
}

// ListPar2Filenames returns the data file names a PAR2 archive protects:
// the recovery set first, then the non-recovery set — the order the file
// description packets are written in. Only the index file is parsed, the
// parity volumes are not touched.
func ListPar2Filenames(path string) ([]string, error) {
	meta, err := parseParSet(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(meta.recoverySet)+len(meta.nonRecoverySet))
	for _, ids := range [][]fileID{meta.recoverySet, meta.nonRecoverySet} {
		for _, id := range ids {
			names = append(names, meta.descriptions[id].filename)
		}
	}
	return names, nil
}

func CalculateBlockSize(fSize uint64, percentage uint64) int {

	// f_size // 2**15
	blockSizeMin := fSize >> 15

	// (f_size * self.percentage) // 100
	blockSizeF := (fSize * percentage) / 100

	maxBlocks := blockCountMax(fSize)

	var blockCount uint64
	var blockSize uint64

	if blockSizeF > blockSizeMin {
		if blockSizeMin == 0 {
			blockCount = 1
			blockSize = blockSizeMin
		} else {
			// blockcount = min(blockcount_max, blocksize_f // blocksize_min)
			calcCount := blockSizeF / blockSizeMin
			if maxBlocks < calcCount {
				blockCount = maxBlocks
			} else {
				blockCount = calcCount
			}
			blockSize = blockSizeF / blockCount
		}
	} else {
		blockSize = 4
	}

	blockSize = (blockSize/4 + 1) * 4

	return int(blockSize)
}

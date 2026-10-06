package par2

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	gopar "github.com/akalin/gopar/par2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateBlockSize_Boundaries pins the block size at the size
// boundaries and beyond. The expected values were captured from the
// implementation before it was refactored into the blockCountLimits table
// (item 3.1): any difference means the thresholds silently changed.
func TestCalculateBlockSize_Boundaries(t *testing.T) {
	tests := []struct {
		fSize uint64
		want  int
	}{
		{0, 8},
		{1, 8},
		{1000, 4},
		{999999, 7144},                   // just under the 1e6 boundary: blockCountMax = 7
		{1000000, 3336},                  // exactly on the 1e6 boundary: blockCountMax = 15
		{3999999, 13336},                 // just under the 4e6 boundary: blockCountMax = 15
		{4000000, 6452},                  // exactly on the 4e6 boundary: blockCountMax = 31
		{19999999, 32260},                // just under the 20e6 boundary: blockCountMax = 31
		{20000000, 7876},                 // exactly on the 20e6 boundary: blockCountMax = 127
		{1000000000, 393704},             // 1 GB
		{math.MaxInt64, 726249766681480}, // huge file: includes the current intermediate overflow
	}

	for _, tc := range tests {
		assert.Equal(t, tc.want, CalculateBlockSize(tc.fSize, 5), "fSize=%d", tc.fSize)
	}
}

func TestCalculateBlockSize_SmallFile(t *testing.T) {
	// File size less than 1MB - blockCountMax = 7
	// fSize = 500000 bytes (< 1e6), percentage = 5
	// blockSizeMin = 500000 >> 15 = 15
	// blockSizeF = (500000 * 5) / 100 = 25000
	// blockSizeF > blockSizeMin, so blockCount = min(7, 25000/15) = min(7, 1666) = 7
	// blockSize = 25000 / 7 = 3571
	// Final: (3571/4 + 1) * 4 = (892 + 1) * 4 = 3572
	result := CalculateBlockSize(500000, 5)
	assert.Equal(t, 3572, result)
}

func TestCalculateBlockSize_VerySmallFile(t *testing.T) {
	// Very small file - less than 32KB (blockSizeMin = 0 or 1)
	// fSize = 1000 bytes, percentage = 5
	// blockSizeMin = 1000 >> 15 = 0
	// blockSizeF = (1000 * 5) / 100 = 50
	// blockSizeF > blockSizeMin (50 > 0), but blockSizeMin == 0
	// So: blockCount = 1, blockSize = blockSizeMin = 0
	// Final: (0/4 + 1) * 4 = (0 + 1) * 4 = 4
	result := CalculateBlockSize(1000, 5)
	assert.Equal(t, 4, result)
}

func TestCalculateBlockSize_MediumFile(t *testing.T) {
	// File size between 1MB and 4MB - blockCountMax = 15
	// fSize = 2000000 bytes (2MB, >= 1e6 but < 4e6), percentage = 5
	// blockSizeMin = 2000000 >> 15 = 61
	// blockSizeF = (2000000 * 5) / 100 = 100000
	// blockCountMax = 15
	// blockCount = min(15, 100000/61) = min(15, 1639) = 15
	// blockSize = 100000 / 15 = 6666
	// Final: (6666/4 + 1) * 4 = (1666 + 1) * 4 = 6668
	result := CalculateBlockSize(2000000, 5)
	assert.Equal(t, 6668, result)
}

func TestCalculateBlockSize_LargeFile(t *testing.T) {
	// File size between 4MB and 20MB - blockCountMax = 31
	// fSize = 10000000 bytes (10MB), percentage = 5
	// blockSizeMin = 10000000 >> 15 = 305
	// blockSizeF = (10000000 * 5) / 100 = 500000
	// blockCountMax = 31
	// blockCount = min(31, 500000/305) = min(31, 1639) = 31
	// blockSize = 500000 / 31 = 16129
	// Final: (16129/4 + 1) * 4 = (4032 + 1) * 4 = 16132
	result := CalculateBlockSize(10000000, 5)
	assert.Equal(t, 16132, result)
}

func TestCalculateBlockSize_VeryLargeFile(t *testing.T) {
	// File size > 20MB - blockCountMax = 127
	// fSize = 100000000 bytes (100MB), percentage = 5
	// blockSizeMin = 100000000 >> 15 = 3051
	// blockSizeF = (100000000 * 5) / 100 = 5000000
	// blockCount = min(127, 5000000/3051) = min(127, 1638) = 127
	// blockSize = 5000000 / 127 = 39370
	// Final: (39370/4 + 1) * 4 = (9842 + 1) * 4 = 39372
	result := CalculateBlockSize(100000000, 5)
	assert.Equal(t, 39372, result)
}

func TestCalculateBlockSize_BlockSizeBelowMinimum(t *testing.T) {
	// When blockSizeF <= blockSizeMin, use blockSize = 4
	// fSize = 100 bytes, percentage = 1
	// blockSizeMin = 100 >> 15 = 0
	// blockSizeF = (100 * 1) / 100 = 1
	// blockSizeF > blockSizeMin (1 > 0), but blockSizeMin == 0
	// So blockSize = 0, then final = (0/4 + 1) * 4 = 4
	result := CalculateBlockSize(100, 1)
	assert.Equal(t, 4, result)
}

func TestCalculateBlockSize_ZeroPercentage(t *testing.T) {
	// fSize = 1000000 bytes, percentage = 0
	// blockSizeMin = 1000000 >> 15 = 30
	// blockSizeF = (1000000 * 0) / 100 = 0
	// blockSizeF <= blockSizeMin (0 <= 30), so blockSize = 4
	// Final: (4/4 + 1) * 4 = (1 + 1) * 4 = 8
	result := CalculateBlockSize(1000000, 0)
	assert.Equal(t, 8, result)
}

func TestCalculateBlockSize_HundredPercent(t *testing.T) {
	// fSize = 500000 bytes (< 1e6), percentage = 100
	// blockSizeMin = 500000 >> 15 = 15
	// blockSizeF = (500000 * 100) / 100 = 500000
	// blockCountMax = 7
	// blockCount = min(7, 500000/15) = min(7, 33333) = 7
	// blockSize = 500000 / 7 = 71428
	// Final: (71428/4 + 1) * 4 = (17857 + 1) * 4 = 71432
	result := CalculateBlockSize(500000, 100)
	assert.Equal(t, 71432, result)
}

func TestCalculateBlockSize_AlignedToFour(t *testing.T) {
	// Test that result is always aligned to 4
	// Any result should be divisible by 4
	testCases := []struct {
		fSize      uint64
		percentage uint64
	}{
		{1000, 5},
		{10000, 5},
		{100000, 5},
		{500000, 5},
		{2000000, 3},
		{2000000, 7},
		{2000000, 10},
		{10000000, 5},
		{100000000, 5},
	}

	for _, tc := range testCases {
		result := CalculateBlockSize(tc.fSize, tc.percentage)
		assert.Equal(t, 0, result%4, "Result should be aligned to 4 for fSize=%d, percentage=%d", tc.fSize, tc.percentage)
	}
}

func TestCalculateBlockSize_Exactly1MB(t *testing.T) {
	// fSize = 1048576 bytes (1 MB = 2^20), percentage = 5
	// blockSizeMin = 1048576 >> 15 = 32
	// blockSizeF = (1048576 * 5) / 100 = 52428
	// blockCountMax = 127 (since 1048576 >= 1e6, but < 4e6)
	// Wait, 1048576 > 1e6 (1000000), so blockCountMax = 15 (since 1048576 < 4e6)
	// blockCount = min(15, 52428/32) = min(15, 1638) = 15
	// blockSize = 52428 / 15 = 3495
	// Final: (3495/4 + 1) * 4 = (873 + 1) * 4 = 3496
	result := CalculateBlockSize(1048576, 5)
	assert.Equal(t, 3496, result)
}

func TestCalculateBlockSize_Exactly4MB(t *testing.T) {
	// fSize = 4194304 bytes (4 MB = 2^22), percentage = 5
	// blockSizeMin = 4194304 >> 15 = 128
	// blockSizeF = (4194304 * 5) / 100 = 209715
	// blockCountMax = 31 (since 4194304 >= 4e6 but < 20e6)
	// blockCount = min(31, 209715/128) = min(31, 1638) = 31
	// blockSize = 209715 / 31 = 6765
	// Final: (6765/4 + 1) * 4 = (1691 + 1) * 4 = 6768
	result := CalculateBlockSize(4194304, 5)
	assert.Equal(t, 6768, result)
}

func TestCalculateBlockSize_Exactly20MB(t *testing.T) {
	// fSize = 20971520 bytes (20 MB), percentage = 5
	// blockSizeMin = 20971520 >> 15 = 640
	// blockSizeF = (20971520 * 5) / 100 = 1048576
	// blockCountMax = 127 (fSize >= 20e6)
	// blockCount = min(127, 1048576/640) = min(127, 1638) = 127
	// blockSize = 1048576 / 127 = 8256
	// Final: (8256/4 + 1) * 4 = (2064 + 1) * 4 = 8260
	result := CalculateBlockSize(20971520, 5)
	assert.Equal(t, 8260, result)
}

func TestCalculateBlockSize_Boundary1MB(t *testing.T) {
	// fSize = 999999 bytes (just under 1MB), percentage = 5
	// blockSizeMin = 999999 >> 15 = 30
	// blockSizeF = (999999 * 5) / 100 = 49999
	// blockCountMax = 7 (since fSize < 1e6)
	// blockCount = min(7, 49999/30) = min(7, 1666) = 7
	// blockSize = 49999 / 7 = 7142
	// Final: (7142/4 + 1) * 4 = (1785 + 1) * 4 = 7144
	result := CalculateBlockSize(999999, 5)
	assert.Equal(t, 7144, result)
}

func TestCalculateBlockSize_Boundary4MB(t *testing.T) {
	// fSize = 3999999 bytes (just under 4MB), percentage = 5
	// blockSizeMin = 3999999 >> 15 = 122
	// blockSizeF = (3999999 * 5) / 100 = 199999
	// blockCountMax = 15 (since fSize < 4e6)
	// blockCount = min(15, 199999/122) = min(15, 1639) = 15
	// blockSize = 199999 / 15 = 13333
	// Final: (13333/4 + 1) * 4 = (3333 + 1) * 4 = 13336
	result := CalculateBlockSize(3999999, 5)
	assert.Equal(t, 13336, result)
}

// Item 5.1: ListPar2Filenames must return the protected data files of a
// real archive; a file that is not a PAR2 archive is an error. The decoder
// pass also exercises every FilenameCollector callback.
func TestListPar2Filenames(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(data, bytes.Repeat([]byte("x"), 64_000), 0o644))

	require.NoError(t, gopar.Create(data+".par2", []string{data}, gopar.CreateOptions{
		SliceByteCount:  CalculateBlockSize(64_000, 5),
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))

	names, err := ListPar2Filenames(data + ".par2")
	require.NoError(t, err)
	assert.Equal(t, []string{"test.txt"}, names)

	// a non-archive file is an error, not an empty list
	_, err = ListPar2Filenames(data)
	assert.Error(t, err)
}

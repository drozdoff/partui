package par2

import (
	"math"

	"github.com/akalin/gopar/par2"
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

type FilenameCollector struct{ names []string }

func (f *FilenameCollector) OnCreatorPacketLoad(clientID string) {}
func (f *FilenameCollector) OnMainPacketLoad(sliceByteCount, recoverySetCount, nonRecoverySetCount int) {
}
func (f *FilenameCollector) OnFileDescriptionPacketLoad(fileID [16]byte, filename string, byteCount int) {
	f.names = append(f.names, filename)
}
func (f *FilenameCollector) OnIFSCPacketLoad(fileID [16]byte)                                     {}
func (f *FilenameCollector) OnRecoveryPacketLoad(exponent uint16, byteCount int)                  {}
func (f *FilenameCollector) OnUnknownPacketLoad(packetType [16]byte, byteCount int)               {}
func (f *FilenameCollector) OnOtherPacketSkip(setID [16]byte, packetType [16]byte, byteCount int) {}
func (f *FilenameCollector) OnDataFileLoad(i, n int, path string, byteCount, hits, misses int, err error) {
}
func (f *FilenameCollector) OnParityFileLoad(i int, path string, err error) {}
func (f *FilenameCollector) OnDetectCorruptDataChunk(fileID [16]byte, path string, startByteOffset, endByteOffset int) {
	// TODO: store corrupt filename to be used later in verify
}
func (f *FilenameCollector) OnDetectDataFileHashMismatch(fileID [16]byte, path string)       {}
func (f *FilenameCollector) OnDetectDataFileWrongByteCount(fileID [16]byte, path string)     {}
func (f *FilenameCollector) OnDataFileWrite(i, n int, path string, byteCount int, err error) {}

func ListPar2Filenames(path string) ([]string, error) {
	collector := &FilenameCollector{}
	_, err := par2.NewDecoder(collector, path, 0)
	if err != nil {
		return nil, err
	}
	return collector.names, nil
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

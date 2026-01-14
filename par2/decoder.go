package par2

import "github.com/akalin/gopar/par2"

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

	var blockCountMax uint64 = 127
	if fSize < 1e6 {
		blockCountMax = 7
	} else if fSize < 4e6 {
		blockCountMax = 15
	} else if fSize < 20e6 {
		blockCountMax = 31
	}

	var blockCount uint64
	var blockSize uint64

	if blockSizeF > blockSizeMin {
		if blockSizeMin == 0 {
			blockCount = 1
			blockSize = blockSizeMin
		} else {
			// blockcount = min(blockcount_max, blocksize_f // blocksize_min)
			calcCount := blockSizeF / blockSizeMin
			if blockCountMax < calcCount {
				blockCount = blockCountMax
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

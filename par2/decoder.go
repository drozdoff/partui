package par2

import "github.com/akalin/gopar/par2"

type filenameCollector struct{ names []string }

func (f *filenameCollector) OnCreatorPacketLoad(clientID string) {}
func (f *filenameCollector) OnMainPacketLoad(sliceByteCount, recoverySetCount, nonRecoverySetCount int) {
}
func (f *filenameCollector) OnFileDescriptionPacketLoad(fileID [16]byte, filename string, byteCount int) {
	f.names = append(f.names, filename)
}
func (f *filenameCollector) OnIFSCPacketLoad(fileID [16]byte)                                     {}
func (f *filenameCollector) OnRecoveryPacketLoad(exponent uint16, byteCount int)                  {}
func (f *filenameCollector) OnUnknownPacketLoad(packetType [16]byte, byteCount int)               {}
func (f *filenameCollector) OnOtherPacketSkip(setID [16]byte, packetType [16]byte, byteCount int) {}
func (f *filenameCollector) OnDataFileLoad(i, n int, path string, byteCount, hits, misses int, err error) {
}
func (f *filenameCollector) OnParityFileLoad(i int, path string, err error) {}
func (f *filenameCollector) OnDetectCorruptDataChunk(fileID [16]byte, path string, startByteOffset, endByteOffset int) {
}
func (f *filenameCollector) OnDetectDataFileHashMismatch(fileID [16]byte, path string)       {}
func (f *filenameCollector) OnDetectDataFileWrongByteCount(fileID [16]byte, path string)     {}
func (f *filenameCollector) OnDataFileWrite(i, n int, path string, byteCount int, err error) {}

func ListPar2Filenames(path string) ([]string, error) {
	collector := &filenameCollector{}
	_, err := par2.NewDecoder(collector, path, 0)
	if err != nil {
		return nil, err
	}
	return collector.names, nil
}

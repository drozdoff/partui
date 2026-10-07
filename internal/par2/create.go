package par2

// Streaming PAR2 creation (plans/streaming_file.md, stage 2). The data
// files are streamed slice by slice instead of being loaded whole: hashes,
// IFSC checksum pairs and the parity contribution of every slice are
// collected in a single pass per file, so memory stays
// O(sliceByteCount + parityShards × sliceByteCount) regardless of file
// size. The output is byte-identical to gopar's Create — the golden tests
// in create_test.go pin that against gopar itself, and TestRepairViaGopar
// proves the parity bytes restore data through gopar's repair.
//
// Deliberate deviations from gopar (see plans/streaming_file.md §6.6):
//   - an empty data file is an error, not a panic in the RS coder;
//   - a failing or cancelled write removes the files this call has written
//     (gopar left partial output behind).

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"

	"partui/internal/par2/rsec16"
)

const (
	// SliceByteCountDefault is used for CreateOptions.SliceByteCount <= 0,
	// matching gopar's default.
	SliceByteCountDefault = 2000
	// NumParityShardsDefault is used for CreateOptions.NumParityShards <= 0,
	// matching gopar's default.
	NumParityShardsDefault = 3
)

// sixteenKByteCount is the file prefix length the sixteenK hash covers.
const sixteenKByteCount = 16 << 10

// clientID goes into the creator packet. gopar's value is kept on purpose:
// byte-identical output is a stage 2 acceptance criterion
// (plans/streaming_file.md §9.2).
const clientID = "gopar"

// writeBufferSize bounds the bufio layer between the packet stream and the
// output files.
const writeBufferSize = 256 << 10

// CreateDelegate receives progress reports from CreateStream.
type CreateDelegate interface {
	// OnProgress is called after every slice has been accumulated; ctx
	// carries the cancellation.
	OnProgress(done, total int)
}

// CreateOptions holds the options for a streaming create. The zero-value
// fields follow the gopar conventions partui relies on: SliceByteCount and
// NumGoroutines <= 0 mean "implementation default", NumParityShards <= 0
// means the default shard count (3).
type CreateOptions struct {
	// SliceByteCount is how big each slice should be in bytes.
	SliceByteCount int
	// NumParityShards is the number of parity shards to create; <= 0 selects
	// the default count.
	NumParityShards int
	// NumGoroutines parallelises the parity computation; <= 0 selects the
	// default.
	NumGoroutines int
	// Delegate is optional; a nil delegate disables progress reporting.
	Delegate CreateDelegate
}

// createFile is the per-input state collected across the passes.
type createFile struct {
	relPath string
	absPath string
	size    int64

	sixteenKHash [md5.Size]byte // actual first sixteenKByteCount bytes
	id           fileID

	hash  [md5.Size]byte // md5 over the actual bytes, filled by the scan
	pairs []checksumPair // per padded slice, filled by the scan
}

// CreateStream creates a PAR2 set for the given files at parPath with one
// streaming pass per data file. The data files must lie in the directory of
// parPath, like in gopar. On error — cancellation included — the files this
// call has written are removed.
func CreateStream(ctx context.Context, parPath string, files []string, opts CreateOptions) error {
	if path.Ext(parPath) != ".par2" {
		return errors.New("parPath must have a .par2 extension")
	}
	if len(files) == 0 {
		return errors.New("filePaths must not be empty")
	}

	sliceByteCount := opts.SliceByteCount
	if sliceByteCount <= 0 {
		sliceByteCount = SliceByteCountDefault
	}
	numParityShards := opts.NumParityShards
	if numParityShards <= 0 {
		numParityShards = NumParityShardsDefault
	}
	numGoroutines := opts.NumGoroutines
	if numGoroutines <= 0 {
		numGoroutines = rsec16.DefaultNumGoroutines()
	}
	if sliceByteCount == 0 || sliceByteCount%4 != 0 {
		return errors.New("invalid slice byte count")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	absParPath, err := filepath.Abs(parPath)
	if err != nil {
		return err
	}
	basePath := filepath.Dir(absParPath)

	// Pass A: stat the inputs and read their sixteenK prefixes — enough to
	// compute the file IDs and the recovery-set order without touching the
	// file bodies.
	sorted, err := scanCreateInputs(basePath, files)
	if err != nil {
		return err
	}

	// Pass B: stream every file once — file hash, IFSC pairs and the parity
	// accumulation, slice by slice.
	parity, err := accumulateParity(ctx, sorted, sliceByteCount, numParityShards, numGoroutines, opts.Delegate)
	if err != nil {
		return err
	}

	// Pass C: write the index and the volume files. The parity buffers are
	// the only large state left at this point.
	return writeParSet(ctx, parPath, sorted, sliceByteCount, parity)
}

// scanCreateInputs validates the input paths the way gopar's newEncoder
// does (absolute base, inputs inside it) and computes the sixteenK hash and
// file ID of every input, reading at most sixteenKByteCount bytes per file.
// An empty data file is an error here — gopar panics in the RS coder for
// one, and the error must surface before any output is written
// (plans/streaming_file.md §6.6.3). The returned slice is ordered by file
// ID, which is the recovery-set order of the main packet.
func scanCreateInputs(basePath string, filePaths []string) ([]*createFile, error) {
	inputs := make([]*createFile, 0, len(filePaths))
	for _, p := range filePaths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		relPath, err := filepath.Rel(basePath, absPath)
		if err != nil {
			return nil, err
		}
		if relPath[0] == '.' {
			return nil, errors.New("data files must lie in basePath")
		}
		// gopar rejects non-ASCII names when writing the description packet,
		// after all the data work; checking here just moves the same error
		// before any output exists.
		if _, err := encodeASCIIString(relPath); err != nil {
			return nil, err
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return nil, err
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("empty data file %q", relPath)
		}

		cf := &createFile{relPath: relPath, absPath: absPath, size: info.Size()}
		cf.sixteenKHash, err = readSixteenKHash(absPath, info.Size())
		if err != nil {
			return nil, err
		}
		cf.id = computeFileID(cf.sixteenKHash, uint64(cf.size), []byte(relPath))
		inputs = append(inputs, cf)
	}

	sort.Slice(inputs, func(i, j int) bool {
		return fileIDLess(inputs[i].id, inputs[j].id)
	})
	return inputs, nil
}

// readSixteenKHash hashes the first min(sixteenKByteCount, size) bytes of
// the file — sixteenKHash of gopar's data_file.go, streaming.
func readSixteenKHash(p string, size int64) ([md5.Size]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return [md5.Size]byte{}, err
	}
	defer f.Close()

	n := min(size, sixteenKByteCount)
	prefix := make([]byte, n)
	if _, err := f.ReadAt(prefix, 0); err != nil {
		return [md5.Size]byte{}, err
	}
	return md5.Sum(prefix), nil
}

// accumulateParity streams every input file once, collecting the file hash,
// the per-slice checksum pairs and the parity. The global slice index — the
// matrix column — runs over the recovery set in file ID order, exactly like
// the dataShards concatenation of gopar's ComputeParityData.
func accumulateParity(ctx context.Context, sorted []*createFile, sliceByteCount, numParityShards, numGoroutines int, dg CreateDelegate) ([][]byte, error) {
	var totalSlices int
	for _, cf := range sorted {
		totalSlices += sliceCount(cf.size, int64(sliceByteCount))
	}

	pm, err := rsec16.NewPAR2Vandermonde(totalSlices, numParityShards, numGoroutines)
	if err != nil {
		return nil, err
	}

	parity := make([][]byte, numParityShards)
	for i := range parity {
		parity[i] = make([]byte, sliceByteCount)
	}

	report := func(done int) {
		if dg != nil {
			dg.OnProgress(done, totalSlices)
		}
	}

	buf := make([]byte, sliceByteCount)
	col := 0
	for _, cf := range sorted {
		used, err := scanCreateFile(ctx, cf, sliceByteCount, buf, parity, pm, col, report)
		if err != nil {
			return nil, err
		}
		col += used
	}
	return parity, nil
}

// scanCreateFile streams one data file: every slice is read, zero-padded to
// sliceByteCount (sliceAndPadByteArray), checksummed into the IFSC pairs,
// accumulated into the parity, and only its actual bytes feed the file
// hash. It returns the number of slices consumed.
func scanCreateFile(ctx context.Context, cf *createFile, sliceByteCount int, buf []byte, parity [][]byte, pm rsec16.ParityMatrix, colBase int, report func(int)) (int, error) {
	f, err := os.Open(cf.absPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var fileHash hash.Hash = md5.New()
	col := colBase
	for off := int64(0); off < cf.size; off += int64(sliceByteCount) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		n := min(cf.size-off, int64(sliceByteCount))
		if _, err := f.ReadAt(buf[:n], off); err != nil {
			return 0, fmt.Errorf("could not read %s: %w", cf.relPath, err)
		}
		// The last slice is zero-padded: the IFSC checksums and the parity
		// see the padded slice, the file hash only the actual bytes.
		padded := buf[:]
		if n < int64(sliceByteCount) {
			clear(buf[n:])
			padded = buf[:sliceByteCount]
		}

		var crcBytes [4]byte
		binary.LittleEndian.PutUint32(crcBytes[:], crc32.ChecksumIEEE(padded))
		cf.pairs = append(cf.pairs, checksumPair{MD5: md5.Sum(padded), CRC32: crcBytes})

		pm.Accumulate(parity, col, padded)

		fileHash.Write(buf[:n])
		col++
		report(col)
	}

	fileHash.Sum(cf.hash[:0])
	return col - colBase, nil
}

// sliceCount counts the padded slices of a size: the ceil division
// sliceAndPadByteArray's loop produces.
func sliceCount(size, sliceByteCount int64) int {
	return int((size + sliceByteCount - 1) / sliceByteCount)
}

// writeParSet writes the index file and the parity volumes. Every volume
// repeats the index packets (creator, main, per-file description and IFSC)
// followed by its own recovery packets — gopar's Encoder.Write layout —
// but streams the packets to the files instead of buffering whole files.
func writeParSet(ctx context.Context, parPath string, sorted []*createFile, sliceByteCount int, parity [][]byte) error {
	mainPacket := mainPacket{
		sliceByteCount: sliceByteCount,
		recoverySet:    fileIDsOf(sorted),
	}
	mainPacketBytes, err := writeMainPacket(mainPacket)
	if err != nil {
		return err
	}
	paddedMainPacketBytes := padPacketBytes(mainPacketBytes)
	setID := recoverySetID(md5.Sum(paddedMainPacketBytes))

	byID := make(map[fileID]*createFile, len(sorted))
	for _, cf := range sorted {
		byID[cf.id] = cf
	}

	// writePackets emits everything but the recovery packets; the volumes
	// append theirs. gopar serializes the packets afresh for every output
	// file — kept here, so each output is written straight to disk.
	writePackets := func(w io.Writer, recovery func(io.Writer) error) error {
		creatorPacketBytes, err := writeCreatorPacket(clientID)
		if err != nil {
			return err
		}
		if err := writeNextPacket(w, setID, creatorPacketType, padPacketBytes(creatorPacketBytes)); err != nil {
			return err
		}
		if err := writeNextPacket(w, setID, mainPacketType, paddedMainPacketBytes); err != nil {
			return err
		}
		for _, id := range mainPacket.recoverySet {
			cf := byID[id]
			description := fileDescriptionPacket{
				hash:         cf.hash,
				sixteenKHash: cf.sixteenKHash,
				byteCount:    int(cf.size),
				filename:     cf.relPath,
			}
			descriptionBytes, err := writeFileDescriptionPacket(id, description)
			if err != nil {
				return err
			}
			if err := writeNextPacket(w, setID, fileDescriptionPacketType, padPacketBytes(descriptionBytes)); err != nil {
				return err
			}

			ifscBytes, err := writeIFSCPacket(id, ifscPacket{checksumPairs: cf.pairs})
			if err != nil {
				return err
			}
			if err := writeNextPacket(w, setID, ifscPacketType, padPacketBytes(ifscBytes)); err != nil {
				return err
			}
		}
		if recovery != nil {
			return recovery(w)
		}
		return nil
	}

	out := &createOutput{}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := out.writeFile(parPath, func(w io.Writer) error {
		return writePackets(w, nil)
	}); err != nil {
		return err
	}

	ext := path.Ext(parPath)
	base := parPath[:len(parPath)-len(ext)]

	// The doubling volume grouping of gopar's Encoder.Write: vol0+01 holds
	// shard 0, vol1+02 shards 1–2, and so on.
	volumeCount := 1
	for i := 0; i < len(parity); {
		if i+volumeCount > len(parity) {
			volumeCount = len(parity) - i
		}
		start, count := i, volumeCount
		if err := ctx.Err(); err != nil {
			return err
		}
		volumePath := fmt.Sprintf("%s.vol%02d+%02d.par2", base, start, count)
		if err := out.writeFile(volumePath, func(w io.Writer) error {
			return writePackets(w, func(w io.Writer) error {
				for exp := start; exp < start+count; exp++ {
					recoveryPacketBytes, err := writeRecoveryPacket(exponent(exp), parity[exp])
					if err != nil {
						return err
					}
					if err := writeNextPacket(w, setID, recoveryPacketType, padPacketBytes(recoveryPacketBytes)); err != nil {
						return err
					}
				}
				return nil
			})
		}); err != nil {
			return err
		}

		i += volumeCount
		volumeCount *= 2
	}
	return nil
}

func fileIDsOf(sorted []*createFile) []fileID {
	ids := make([]fileID, len(sorted))
	for i, cf := range sorted {
		ids[i] = cf.id
	}
	return ids
}

// createOutput tracks the files a CreateStream has written, so a failure
// halfway through leaves no partial PAR2 set behind — gopar left truncated
// output on the disk (plans/streaming_file.md §6.6.2).
type createOutput struct {
	written []string
}

// writeFile creates p (truncating any previous content) and streams the
// packets into it. On error — including a failed open — every file written
// so far is removed.
func (o *createOutput) writeFile(p string, fill func(w io.Writer) error) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		o.written = append(o.written, p)

		bw := bufio.NewWriterSize(f, writeBufferSize)
		err = fill(bw)
		if err == nil {
			err = bw.Flush()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		o.cleanup()
	}
	return err
}

func (o *createOutput) cleanup() {
	for _, p := range o.written {
		os.Remove(p)
	}
	o.written = nil
}

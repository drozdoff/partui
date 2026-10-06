package par2

// Streaming PAR2 verification (plans/streaming_file.md, stage 1). The data
// files of a PAR2 set are streamed through a sliding window instead of being
// loaded whole, so memory stays O(sliceByteCount) regardless of file size.
// The statuses and shard counts mirror gopar's Verify byte-for-byte; the
// oracle tests in verify_oracle_test.go pin that against gopar itself.

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
)

// parSetMeta is everything needed to verify one PAR2 set. partui always
// works with one data file per set, but the format is kept general.
type parSetMeta struct {
	setID          recoverySetID
	clientID       string
	sliceByteCount int
	recoverySet    []fileID // in main packet order
	nonRecoverySet []fileID
	descriptions   map[fileID]fileDescriptionPacket
	ifsc           map[fileID][]checksumPair
	// parityExponents is the number of recovery shards present across all
	// volumes: the highest exponent seen plus one, nil gaps included.
	parityExponents int
}

// parseParSet reads the index file and collects the metadata needed for
// verification. Only the index is parsed here — parity volumes are
// validated separately by loadParity, so listing filenames costs no volume
// IO. Error semantics mirror gopar's newDecoder.
func parseParSet(indexPath string) (*parSetMeta, error) {
	indexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}

	setID, f, err := readPackets(indexBytes, nil)
	if err != nil {
		return nil, err
	}

	if f.mainPacket == nil {
		return nil, errors.New("no main packet found")
	}

	if len(f.recovery) > 0 {
		return nil, errors.New("recovery packets found in index file")
	}

	m := &parSetMeta{
		setID:          setID,
		clientID:       f.clientID,
		sliceByteCount: f.mainPacket.sliceByteCount,
		recoverySet:    f.mainPacket.recoverySet,
		nonRecoverySet: f.mainPacket.nonRecoverySet,
		descriptions:   f.descriptions,
		ifsc:           make(map[fileID][]checksumPair),
	}

	// Every file of the recovery and the non-recovery set needs a
	// description and an IFSC packet (gopar's makeDecoderInputFileInfos).
	for _, ids := range [][]fileID{m.recoverySet, m.nonRecoverySet} {
		for _, id := range ids {
			if _, ok := f.descriptions[id]; !ok {
				return nil, errors.New("file description packet not found")
			}
			ifsc, ok := f.ifsc[id]
			if !ok {
				return nil, errors.New("input file slice checksum packet not found")
			}
			m.ifsc[id] = ifsc.checksumPairs
		}
	}

	return m, nil
}

// loadParity globs the parity volumes next to the index file and validates
// them the way gopar's LoadParityData does. The recovery shard bytes
// themselves are not needed for verification and are discarded.
func (m *parSetMeta) loadParity(indexPath string) error {
	ext := path.Ext(indexPath)
	base := indexPath[:len(indexPath)-len(ext)]
	matches, err := filepath.Glob(base + ".*" + ext)
	if err != nil {
		return err
	}

	maxExponent := -1
	for _, match := range matches {
		volumeBytes, err := os.ReadFile(match)
		if err != nil {
			return err
		}

		_, f, err := readPackets(volumeBytes, &m.setID)
		if errors.Is(err, errNoPacketsFound) {
			// Not a parity volume (e.g. an unrelated file matching the
			// glob, or the index itself in a degenerate case).
			continue
		} else if err != nil {
			return err
		}

		if f.mainPacket == nil {
			// gopar dereferences a nil main packet here and would panic;
			// a corrupt volume is an error, not a crash.
			return errors.New("no main packet found")
		}

		if m.sliceByteCount != f.mainPacket.sliceByteCount {
			return errors.New("slice byte count mismatch")
		}

		if !equalFileIDs(m.recoverySet, f.mainPacket.recoverySet) {
			return errors.New("recovery set mismatch")
		}

		if !equalFileIDs(m.nonRecoverySet, f.mainPacket.nonRecoverySet) {
			return errors.New("non-recovery set mismatch")
		}

		for exp := range f.recovery {
			if int(exp) > maxExponent {
				maxExponent = int(exp)
			}
		}
	}

	m.parityExponents = maxExponent + 1
	return nil
}

func equalFileIDs(a, b []fileID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FileResult is the verification outcome of one data file of the set.
type FileResult struct {
	Path           string
	Missing        bool
	HashMismatch   bool
	WrongByteCount bool
	UsableShards   int
	// UnusableShards counts expected slices whose checksum pair was not
	// found anywhere in the set's data files.
	UnusableShards int
	// RepairNeeded mirrors gopar's ShardCounts.RepairNeeded: some expected
	// slice is unusable. Note that HashMismatch and WrongByteCount do not
	// influence it — that is gopar semantics, reproduced deliberately.
	RepairNeeded bool
}

// VerifyDelegate receives progress reports from VerifyStream.
type VerifyDelegate interface {
	// OnProgress is called whenever expected slices get resolved; ctx
	// carries the cancellation.
	OnProgress(done, total int)
}

// VerifyOptions holds the options for a streaming verify.
type VerifyOptions struct {
	// Delegate is optional; a nil delegate disables progress reporting.
	Delegate VerifyDelegate
}

// VerifyResult is the aggregate result of a streaming verify.
type VerifyResult struct {
	RepairNeeded bool
}

// shardRef identifies one expected slice: the recovery-set file it belongs
// to and the slice's index in that file's IFSC packet.
type shardRef struct {
	fileIdx  int
	sliceIdx int
}

// checksumMap is gopar's checksumShardLocationMap: crc32 → md5 → the
// expected slices having that pair.
type checksumMap map[uint32]map[[md5.Size]byte][]shardRef

func newChecksumMap(m *parSetMeta) checksumMap {
	cm := make(checksumMap)
	for i, id := range m.recoverySet {
		for j, pair := range m.ifsc[id] {
			cm.put(binary.LittleEndian.Uint32(pair.CRC32[:]), pair.MD5, shardRef{i, j})
		}
	}
	return cm
}

func (cm checksumMap) put(crc uint32, h [md5.Size]byte, ref shardRef) {
	byHash := cm[crc]
	if byHash == nil {
		byHash = make(map[[md5.Size]byte][]shardRef)
		cm[crc] = byHash
	}
	byHash[h] = append(byHash[h], ref)
}

// byCRC returns the md5→locations submap for a window crc32, or nil when no
// expected slice shares it — the md5 of the window is only computed in that
// case (mirrors gopar's checksumShardLocationMap.get short-circuit).
func (cm checksumMap) byCRC(crc uint32) map[[md5.Size]byte][]shardRef {
	return cm[crc]
}

// scanState tracks which expected slices have been found so far — one bool
// per expected slice of every recovery-set file. A hit in one file can mark
// slices of any file (the map is global), mirroring gopar's "usable ⇔ the
// pair was found anywhere in the set".
type scanState struct {
	found [][]bool
	done  int // progress counter: resolved expected slices
	total int
}

func newScanState(m *parSetMeta) *scanState {
	st := &scanState{found: make([][]bool, len(m.recoverySet))}
	for i, id := range m.recoverySet {
		st.found[i] = make([]bool, len(m.ifsc[id]))
		st.total += len(st.found[i])
	}
	return st
}

// mark records that the expected slice ref was found somewhere; it reports
// whether this was the first time (i.e. progress advanced).
func (st *scanState) mark(ref shardRef) bool {
	if st.found[ref.fileIdx][ref.sliceIdx] {
		return false
	}
	st.found[ref.fileIdx][ref.sliceIdx] = true
	st.done++
	return true
}

func (st *scanState) foundCount(fileIdx int) int {
	n := 0
	for _, ok := range st.found[fileIdx] {
		if ok {
			n++
		}
	}
	return n
}

// progress clamps done at total: a file already counted as fully resolved
// (missing) can still gain cross-file marks afterwards.
func (st *scanState) progress() (int, int) {
	return min(st.done, st.total), st.total
}

type scanOutcome struct {
	missing        bool
	hashMismatch   bool
	wrongByteCount bool
}

// padReader serves bytes of a file zero-padded past its end — the "virtual"
// byte array the sliding window slides over (gopar's sliceAndPadByteArray
// pads the tail of the last slice with zeros). Single-byte reads are served
// from two independent read-ahead cursors: during a miss run the window
// tail and the leader byte leaving the window advance in lockstep but sit
// sliceByteCount apart, so one shared buffer would be reloaded on every
// step.
type padReader struct {
	f    *os.File
	size int64
	bufs [2][]byte
	base [2]int64
	n    [2]int
}

const padBufSize = 64 << 10

const (
	leaderCursor  = iota // the byte leaving the window
	trailerCursor        // the byte entering the window
)

func newPadReader(f *os.File, size int64) *padReader {
	return &padReader{
		f:    f,
		size: size,
		bufs: [2][]byte{make([]byte, padBufSize), make([]byte, padBufSize)},
	}
}

// at returns the byte at off; offsets past the end of the file read as 0.
func (r *padReader) at(off int64, cursor int) (byte, error) {
	if off < 0 || off >= r.size {
		return 0, nil
	}
	if off < r.base[cursor] || off >= r.base[cursor]+int64(r.n[cursor]) {
		n, err := r.f.ReadAt(r.bufs[cursor], off)
		r.base[cursor], r.n[cursor] = off, n
		if err != nil && err != io.EOF {
			return 0, err
		}
	}
	return r.bufs[cursor][off-r.base[cursor]], nil
}

// readVirtual fills dst with the virtual bytes [off, off+len(dst)): actual
// file bytes first, zeros past the end.
func (r *padReader) readVirtual(dst []byte, off int64) error {
	if off >= r.size {
		clear(dst)
		return nil
	}
	n, err := r.f.ReadAt(dst, off)
	if n < len(dst) {
		clear(dst[n:])
	}
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

const hashFeedChunk = 32 << 10

// scanFile streams the file at p through the checksum map and marks every
// expected slice whose checksum pair occurs anywhere — gopar's
// fillShardInfos semantics: the window is sliceByteCount wide, a hit
// advances it a whole slice, a miss advances it one byte (incremental
// crc32; the md5 of the window is recomputed only when its crc32 belongs
// to some expected slice). The md5 of the whole file and the sixteenK hash
// are computed over the actual bytes on the way. Memory: one window plus
// small buffers, independent of the file size.
func scanFile(ctx context.Context, p string, id fileID, m *parSetMeta, cm checksumMap, st *scanState, onProgress func()) (scanOutcome, error) {
	var out scanOutcome

	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			out.missing = true
			return out, nil
		}
		return out, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return out, err
	}
	size := info.Size()

	n := int64(m.sliceByteCount)
	crcWindow := newCRC32Window(m.sliceByteCount)
	r := newPadReader(f, size)
	win := make([]byte, n)
	fileHash, sixteenK := md5.New(), md5.New()
	sixteenKLeft := 16 << 10
	feed := make([]byte, hashFeedChunk)

	hashed := int64(0) // actual bytes [0, hashed) already fed to the hashes
	var crc uint32
	justMissed := false

	for j := int64(0); j < size; {
		if err := ctx.Err(); err != nil {
			return out, err
		}

		if justMissed {
			// O(1) incremental crc of the window shifted by one byte; the
			// window buffer itself is stale here and is only re-read (below)
			// when its md5 is actually needed — a miss run costs no per-byte
			// window traffic, matching gopar's in-memory indexing
			leader, err := r.at(j-1, leaderCursor)
			if err != nil {
				return out, err
			}
			trailer, err := r.at(j+n-1, trailerCursor)
			if err != nil {
				return out, err
			}
			crc = crcWindow.update(crc, leader, trailer)
		} else {
			if err := r.readVirtual(win, j); err != nil {
				return out, err
			}
			crc = crc32.ChecksumIEEE(win)
		}

		doneBefore := st.done
		byHash := cm.byCRC(crc)
		switch {
		case len(byHash) == 0:
			// no expected slice shares this crc32 — md5 not even computed
			justMissed = true
			j++
		default:
			// md5 needed: win must hold virtual[j : j+n)
			if justMissed {
				if err := r.readVirtual(win, j); err != nil {
					return out, err
				}
			}
			if refs := byHash[md5.Sum(win)]; len(refs) > 0 {
				for _, ref := range refs {
					st.mark(ref)
				}
				justMissed = false
				j += n
			} else {
				justMissed = true
				j++
			}
		}

		// Feed the actual bytes that left the covered prefix to the file
		// hashes; the window at the new j covers [j, j+n) virtually, so
		// exactly [hashed, j) is now settled actual data.
		limit := min(j, size)
		for hashed < limit {
			k := min(int64(len(feed)), limit-hashed)
			buf := feed[:k]
			if err := r.readVirtual(buf, hashed); err != nil {
				return out, err
			}
			fileHash.Write(buf)
			if sixteenKLeft > 0 {
				sk := min(sixteenKLeft, int(k))
				sixteenK.Write(buf[:sk])
				sixteenKLeft -= sk
			}
			hashed += k
		}

		if st.done != doneBefore && onProgress != nil {
			onProgress()
		}
	}

	var gotFile, got16 [md5.Size]byte
	fileHash.Sum(gotFile[:0])
	sixteenK.Sum(got16[:0])

	desc := m.descriptions[id]
	out.hashMismatch = got16 != desc.sixteenKHash || gotFile != desc.hash
	out.wrongByteCount = size != int64(desc.byteCount)
	return out, nil
}

// VerifyStream verifies the PAR2 set at indexPath with memory bounded by
// O(sliceByteCount): the index and the parity volumes are parsed for
// metadata, the data files are streamed through a sliding window. The
// returned results follow the recovery set order; an error means the set
// could not be processed at all (gopar's Verify leaves the result empty in
// that case, and so does this). ctx is checked at every window step, so
// cancellation is bounded by one slice pass.
func VerifyStream(ctx context.Context, indexPath string, dg VerifyDelegate) ([]FileResult, error) {
	if path.Ext(indexPath) != ".par2" {
		return nil, errors.New("parPath must have a .par2 extension")
	}

	meta, err := parseParSet(indexPath)
	if err != nil {
		return nil, err
	}

	st := newScanState(meta)
	cm := newChecksumMap(meta)
	if dg == nil {
		dg = doNothingVerifyDelegate{}
	}
	report := func() {
		done, total := st.progress()
		dg.OnProgress(done, total)
	}

	baseDir := filepath.Dir(indexPath)
	results := make([]FileResult, len(meta.recoverySet))
	for i, id := range meta.recoverySet {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		p := filepath.Join(baseDir, meta.descriptions[id].filename)
		out, err := scanFile(ctx, p, id, meta, cm, st, report)
		if err != nil {
			return nil, err
		}
		results[i] = FileResult{
			Path:           p,
			Missing:        out.missing,
			HashMismatch:   out.hashMismatch,
			WrongByteCount: out.wrongByteCount,
		}
		if out.missing {
			// A missing file resolves none of its slices by scanning;
			// count them as accounted for in the progress.
			st.done += len(st.found[i])
			report()
		}
	}

	// gopar order: data files first, parity volumes afterwards — a corrupt
	// volume makes the set unverifiable even when the data is intact.
	if err := meta.loadParity(indexPath); err != nil {
		return nil, err
	}

	for i := range results {
		results[i].UsableShards = st.foundCount(i)
		results[i].UnusableShards = len(st.found[i]) - results[i].UsableShards
		results[i].RepairNeeded = results[i].UnusableShards > 0
	}
	report()

	return results, nil
}

type doNothingVerifyDelegate struct{}

func (doNothingVerifyDelegate) OnProgress(done, total int) {}

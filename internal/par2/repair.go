package par2

// Streaming PAR2 repair (plans/repair.md, TODO item 1). The damaged or
// missing slices of a set are reconstructed from the parity shards with the
// same systematic Reed–Solomon code the create writes — but streaming, one
// slice at a time, where gopar's Repair holds every file and shard in
// memory:
//
//  1. scan   — the verify pass, plus the location of every found slice;
//  2. plan   — parity availability, the U×U inverse of the parity
//     submatrix over the missing slices;
//  3. syndromes — each used parity shard minus the contributions of the
//     found slices, accumulated slice by slice from their locations;
//  4. write  — the missing slices are inv × syndromes; damaged files are
//     reassembled into a temp file and renamed over the original only if
//     the description hashes check out.
//
// Memory: O(U × sliceByteCount) for the syndromes and recovered slices plus
// the U×U matrix, with U bounded by maxRepairableShards — independent of
// the file sizes. ctx is checked per slice; a failed or cancelled repair
// never touches the files on disk.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"

	"partui/internal/par2/gf2p16"
	"partui/internal/par2/rsec16"
)

// ErrNotEnoughParity is returned when the set has fewer usable parity
// shards than there are damaged slices — the damage exceeds the redundancy.
var ErrNotEnoughParity = errors.New("not enough parity shards")

// maxRepairableShards caps the damage a single repair may target: the U×U
// reconstruction matrix and U syndrome buffers are held in memory, and the
// default create (three parity shards) keeps U at 3 anyway. Sets with more
// redundancy than the cap are beyond a streaming repair.
const maxRepairableShards = 4096

// RepairDelegate receives progress reports from RepairStream.
type RepairDelegate interface {
	// OnScanProgress reports the verify-style scan of the data files.
	OnScanProgress(done, total int)
	// OnWriteProgress reports the reassembly of the damaged files, in
	// files: done of total files have been rewritten.
	OnWriteProgress(done, total int)
}

// RepairOptions holds the options for a streaming repair.
type RepairOptions struct {
	// Delegate is optional; a nil delegate disables progress reporting.
	Delegate RepairDelegate
	// NumGoroutines parallelises the syndrome accumulation; <= 0 selects
	// the default.
	NumGoroutines int
}

// RepairResult is the aggregate result of a streaming repair.
type RepairResult struct {
	// RepairNeeded mirrors verify's FileResult.RepairNeeded: some expected
	// slice was unusable. A repair that only reassembled drifted files
	// (hash or byte count mismatch, all slices intact) reports false.
	RepairNeeded bool
	// RepairedFiles are the paths rewritten by this repair, recovery-set
	// order.
	RepairedFiles []string
}

// repairScanDelegate routes the shared scan progress into the repair
// delegate's scan callback.
type repairScanDelegate struct{ dg RepairDelegate }

func (d repairScanDelegate) OnProgress(done, total int) {
	if d.dg != nil {
		d.dg.OnScanProgress(done, total)
	}
}

// RepairStream repairs the PAR2 set at indexPath from its parity volumes.
// The set is scanned exactly like VerifyStream first; a set with no damage
// comes back with an empty RepairedFiles and no writes. An error means
// nothing was repaired (the planning happens before any file is touched,
// and every rewritten file is validated against its description hashes
// before it replaces the original).
func RepairStream(ctx context.Context, indexPath string, opts RepairOptions) (RepairResult, error) {
	if path.Ext(indexPath) != ".par2" {
		return RepairResult{}, errors.New("parPath must have a .par2 extension")
	}

	meta, err := parseParSet(indexPath)
	if err != nil {
		return RepairResult{}, err
	}
	baseDir := filepath.Dir(indexPath)

	// Phase 1: the verify scan, plus the location of every found slice.
	st, outcomes, err := scanDataSet(ctx, meta, baseDir, repairScanDelegate{dg: opts.Delegate})
	if err != nil {
		return RepairResult{}, err
	}

	// The missing slices, in the global column order of the encoding
	// matrix: recovery-set order, slice order within a file — the order
	// create accumulated the parity in.
	var missingRows []int
	col := 0
	for i := range meta.recoverySet {
		for j, ok := range st.found[i] {
			if !ok {
				missingRows = append(missingRows, col+j)
			}
		}
		col += len(st.found[i])
	}

	if len(missingRows) == 0 {
		// Nothing to reconstruct. Files whose bytes or size drifted from
		// the description (gopar's !ok files) are still rewritten from
		// their own slices, like gopar's Repair does.
		repaired, err := rewriteDamagedFiles(ctx, meta, baseDir, st, outcomes, nil, nil, opts.Delegate)
		return RepairResult{RepairedFiles: repaired}, err
	}

	// Phase 2: plan — parity availability, then the reconstruction matrix.
	// Nothing has been written yet: an unrepairable set fails here whole.
	if err := meta.loadParity(indexPath); err != nil {
		return RepairResult{}, err
	}

	exps := make([]int, 0, len(meta.parityVolumes))
	for exp := range meta.parityVolumes {
		exps = append(exps, int(exp))
	}
	slices.Sort(exps)
	if len(missingRows) > len(exps) {
		return RepairResult{}, fmt.Errorf("%w: %d damaged slices, %d parity shards present",
			ErrNotEnoughParity, len(missingRows), len(exps))
	}
	if len(missingRows) > maxRepairableShards {
		return RepairResult{}, fmt.Errorf("too many damaged slices: %d exceeds the repair limit of %d",
			len(missingRows), maxRepairableShards)
	}
	usedParityRows := exps[:len(missingRows)] // gopar order: the lowest exponents first

	numGoroutines := opts.NumGoroutines
	if numGoroutines <= 0 {
		numGoroutines = rsec16.DefaultNumGoroutines()
	}
	pm, err := rsec16.NewPAR2Vandermonde(st.total, meta.parityExponents, numGoroutines)
	if err != nil {
		return RepairResult{}, err
	}
	inv, err := pm.ReconstructionMatrix(missingRows, usedParityRows)
	if err != nil {
		return RepairResult{}, fmt.Errorf("reconstruction matrix: %w", err)
	}

	// Phase 3: syndromes. Every used parity shard starts its syndrome
	// buffer (read straight out of its volume packet), then the
	// contribution of every found slice is subtracted — read from where
	// the scan found it, which may be another file of the set.
	U := len(missingRows)
	syndromes := make([][]byte, U)
	for k, row := range usedParityRows {
		if err := ctx.Err(); err != nil {
			return RepairResult{}, err
		}
		shard, err := readParityShard(meta.parityVolumes[exponent(row)], exponent(row), meta.setID, meta.sliceByteCount)
		if err != nil {
			return RepairResult{}, err
		}
		syndromes[k] = shard
	}

	files := newFileCache(meta, baseDir)
	defer files.closeAll()

	buf := make([]byte, meta.sliceByteCount)
	col = 0
	for i := range meta.recoverySet {
		for j, ok := range st.found[i] {
			if !ok {
				continue
			}
			if err := ctx.Err(); err != nil {
				return RepairResult{}, err
			}
			if err := files.readSliceAt(st.loc[i][j], buf); err != nil {
				return RepairResult{}, err
			}
			pm.AccumulateSelected(usedParityRows, syndromes, col+j, buf)
		}
		col += len(st.found[i])
	}

	// Phase 4: the recovered slices, then the files.
	recovered := make([][]byte, U)
	for j := range recovered {
		recovered[j] = make([]byte, meta.sliceByteCount)
		for k := range syndromes {
			gf2p16.MulAndAddByteSliceLE(inv.At(j, k), syndromes[k], recovered[j])
		}
	}
	missingPos := make(map[int]int, U)
	for pos, row := range missingRows {
		missingPos[row] = pos
	}

	repaired, err := rewriteDamagedFiles(ctx, meta, baseDir, st, outcomes, missingPos, recovered, opts.Delegate)
	if err != nil {
		return RepairResult{}, err
	}
	return RepairResult{RepairNeeded: true, RepairedFiles: repaired}, nil
}

// fileNeedsRewrite reports whether the file's bytes on disk differ from
// what the set describes: it is missing, drifted (hash or byte count), or
// has slices the scan could not find anywhere.
func fileNeedsRewrite(st *scanState, outcomes []scanOutcome, i int) bool {
	out := outcomes[i]
	return out.missing || out.hashMismatch || out.wrongByteCount || st.foundCount(i) < len(st.found[i])
}

// rewriteDamagedFiles reassembles every file that needs it (fileNeedsRewrite)
// from its found slices and the recovered ones. missingPos/recovered may be
// nil when nothing had to be reconstructed — then every slice of every
// rewritten file must be found.
func rewriteDamagedFiles(ctx context.Context, meta *parSetMeta, baseDir string, st *scanState, outcomes []scanOutcome, missingPos map[int]int, recovered [][]byte, dg RepairDelegate) ([]string, error) {
	total := 0
	for i := range meta.recoverySet {
		if fileNeedsRewrite(st, outcomes, i) {
			total++
		}
	}
	if dg != nil {
		dg.OnWriteProgress(0, total)
	}

	files := newFileCache(meta, baseDir)
	defer files.closeAll()

	var repaired []string
	col := 0
	for i, id := range meta.recoverySet {
		sliceBase := col
		col += len(st.found[i])

		if !fileNeedsRewrite(st, outcomes, i) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return repaired, err
		}

		p := filepath.Join(baseDir, meta.descriptions[id].filename)
		if err := rewriteFile(ctx, p, meta.descriptions[id], meta.sliceByteCount, i, sliceBase, st, missingPos, recovered, files); err != nil {
			return repaired, err
		}
		repaired = append(repaired, p)

		if dg != nil {
			dg.OnWriteProgress(len(repaired), total)
		}
	}
	return repaired, nil
}

// rewriteFile reassembles the file p from its found slices (read from their
// scan locations) and the recovered ones, into a temporary file next to p.
// The rename over p happens only after the reassembled bytes match the
// description packet's sixteenK and full hashes — gopar verified the whole
// reassembled buffer before writing it; the temp file is the streaming
// equivalent, so a failed repair never makes the original worse.
func rewriteFile(ctx context.Context, p string, desc fileDescriptionPacket, sliceByteCount int, fileIdx, sliceBase int, st *scanState, missingPos map[int]int, recovered [][]byte, files *fileCache) error {
	sliceTotal := len(st.found[fileIdx])

	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".repair-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	replaced := false
	defer func() {
		if !replaced {
			os.Remove(tmpName)
		}
	}()

	bw := bufio.NewWriterSize(tmp, writeBufferSize)
	fileHash, sixteenK := md5.New(), md5.New()
	sixteenKLeft := 16 << 10

	buf := make([]byte, sliceByteCount)
	var written int64 // actual bytes reassembled so far
	for j := range sliceTotal {
		if err := ctx.Err(); err != nil {
			return err
		}

		var s []byte
		if st.found[fileIdx][j] {
			if err := files.readSliceAt(st.loc[fileIdx][j], buf); err != nil {
				return err
			}
			s = buf
		} else {
			s = recovered[missingPos[sliceBase+j]]
		}

		// The last slice carries the zero padding of the checksummed
		// form; only the actual bytes are written and hashed.
		n := min(int64(desc.byteCount)-written, int64(sliceByteCount))
		if _, err := bw.Write(s[:n]); err != nil {
			return err
		}
		fileHash.Write(s[:n])
		if sixteenKLeft > 0 {
			sk := min(int64(sixteenKLeft), n)
			sixteenK.Write(s[:sk])
			sixteenKLeft -= int(sk)
		}
		written += n
	}

	if err := bw.Flush(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	var gotFile, got16 [md5.Size]byte
	fileHash.Sum(gotFile[:0])
	sixteenK.Sum(got16[:0])
	switch {
	case got16 != desc.sixteenKHash:
		return errors.New("hash mismatch (16k) in reconstructed data")
	case gotFile != desc.hash:
		return errors.New("hash mismatch in reconstructed data")
	}

	if err := os.Rename(tmpName, p); err != nil {
		return err
	}
	replaced = true
	return nil
}

// fileCache lazily opens the data files of the set and keeps every handle
// until closeAll: the slice locations of a repair point all over the set,
// and one handle per recovery-set file is a small, bounded set. A location
// never points at a file the scan could not open, so the lazy open cannot
// fail there — errors surface as ordinary read failures.
type fileCache struct {
	meta    *parSetMeta
	baseDir string
	files   map[int]*os.File
}

func newFileCache(meta *parSetMeta, baseDir string) *fileCache {
	return &fileCache{meta: meta, baseDir: baseDir, files: make(map[int]*os.File)}
}

func (fc *fileCache) get(fileIdx int) (*os.File, error) {
	if f, ok := fc.files[fileIdx]; ok {
		return f, nil
	}
	p := filepath.Join(fc.baseDir, fc.meta.descriptions[fc.meta.recoverySet[fileIdx]].filename)
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	fc.files[fileIdx] = f
	return f, nil
}

// readSliceAt fills buf with the virtual bytes at loc (zero-padded past the
// end of the file — the last slice is matched and stored in its padded form,
// so the locations of a repair can reach past the physical end).
func (fc *fileCache) readSliceAt(loc sliceLoc, buf []byte) error {
	f, err := fc.get(loc.file)
	if err != nil {
		return err
	}
	n, err := f.ReadAt(buf, loc.off)
	if n < len(buf) {
		clear(buf[n:])
	}
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

func (fc *fileCache) closeAll() {
	for _, f := range fc.files {
		f.Close()
	}
	fc.files = nil
}

// readParityShard extracts the shard bytes of one recovery packet from a
// volume without loading the volume: the packets are walked by header and
// everything but the wanted body is skipped with a seek. The body's packet
// hash is verified — the volume was validated by loadParity before, and the
// shard read here must be exactly the bytes that validation saw.
func readParityShard(volumePath string, want exponent, setID recoverySetID, sliceByteCount int) ([]byte, error) {
	f, err := os.Open(volumePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hdr := make([]byte, headerSize)
	var off int64
	for {
		_, err := f.ReadAt(hdr, off)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("recovery packet for exponent %d not found in %s", want, volumePath)
			}
			return nil, err
		}
		h, err := readPacketHeader(bytes.NewBuffer(hdr))
		if err != nil {
			return nil, err
		}

		bodyLength := int64(h.Length) - headerSize
		if h.Type == recoveryPacketType && h.RecoverySetID == setID {
			body := make([]byte, bodyLength)
			if _, err := f.ReadAt(body, off+headerSize); err != nil {
				return nil, err
			}
			if computePacketHash(h.RecoverySetID, h.Type, body) != h.Hash {
				return nil, errors.New("hash mismatch")
			}
			exp, err := readRecoveryPacket(body)
			if err != nil {
				return nil, err
			}
			if exp == want {
				shard := body[4:]
				if len(shard) != sliceByteCount {
					return nil, fmt.Errorf("recovery shard size mismatch: %d != %d", len(shard), sliceByteCount)
				}
				return shard, nil
			}
		}
		off += int64(h.Length)
	}
}

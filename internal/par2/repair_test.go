package par2

// Tests of the streaming repair (plans/repair.md §5): the damage cases the
// app sees (a corrupt slice, a lost or truncated file, a drifted file), the
// failure cases (more damage than parity, no or broken volumes), the
// no-op path, and the cross-oracle — gopar must repair what we cannot
// distinguish from our own output.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gopar "github.com/akalin/gopar/par2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// corruptAt flips one byte at the given offset of the file at p.
func corruptAt(t testing.TB, p string, off int64) {
	t.Helper()
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	raw[off] ^= 0xFF
	require.NoError(t, os.WriteFile(p, raw, 0o644))
}

// assertVerifiedOK runs VerifyStream and demands a clean set.
func assertVerifiedOK(t testing.TB, parPath string) {
	t.Helper()
	results, err := VerifyStream(context.Background(), parPath, nil)
	require.NoError(t, err)
	for _, r := range results {
		assert.False(t, r.RepairNeeded, "%s must not need repair after the fix", r.Path)
		assert.False(t, r.Missing, "%s must exist after the fix", r.Path)
		assert.False(t, r.HashMismatch, "%s must match its hashes after the fix", r.Path)
		assert.False(t, r.WrongByteCount, "%s must match its byte count after the fix", r.Path)
	}
}

func TestRepairStream_CorruptSlice(t *testing.T) {
	dir := t.TempDir()
	const size = 100_000 // 25 slices at testSlice; 3 shards repair one
	data := writeFixture(t, dir, "data.bin", size, 1)
	original, err := os.ReadFile(data)
	require.NoError(t, err)

	corruptAt(t, data, int64(size/2))

	res, err := RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assert.True(t, res.RepairNeeded)
	assert.Equal(t, []string{data}, res.RepairedFiles)

	repaired, err := os.ReadFile(data)
	require.NoError(t, err)
	assert.Equal(t, original, repaired, "the repair must restore the original content")
	assertVerifiedOK(t, data+".par2")
}

func TestRepairStream_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// 3 slices against 3 parity shards: a wholly lost file is exactly at
	// the redundancy limit, padding included
	original := randomBytes(2*testSlice+17, 2)
	data := filepath.Join(dir, "data.bin")
	require.NoError(t, os.WriteFile(data, original, 0o644))
	require.NoError(t, gopar.Create(data+".par2", []string{data}, gopar.CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))
	require.NoError(t, os.Remove(data))

	res, err := RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assert.True(t, res.RepairNeeded)
	assert.Equal(t, []string{data}, res.RepairedFiles)

	repaired, err := os.ReadFile(data)
	require.NoError(t, err)
	assert.Equal(t, original, repaired, "the lost file must be rebuilt from parity")
	assertVerifiedOK(t, data+".par2")
}

func TestRepairStream_TruncatedFile(t *testing.T) {
	dir := t.TempDir()
	const size = 5 * testSlice
	data := writeFixture(t, dir, "data.bin", size, 3)
	original, err := os.ReadFile(data)
	require.NoError(t, err)

	// cut two slices off the end
	require.NoError(t, os.WriteFile(data, original[:size-2*testSlice], 0o644))

	res, err := RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assert.True(t, res.RepairNeeded)

	repaired, err := os.ReadFile(data)
	require.NoError(t, err)
	assert.Equal(t, original, repaired)
	assertVerifiedOK(t, data+".par2")
}

func TestRepairStream_TrailingGarbage(t *testing.T) {
	dir := t.TempDir()
	const size = 2 * testSlice
	data := writeFixture(t, dir, "data.bin", size, 4)

	// every slice still verifies, but the file grew: gopar's Repair
	// rewrites !ok files even with no unusable shard
	f, err := os.OpenFile(data, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write([]byte("garbage"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	res, err := RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assert.False(t, res.RepairNeeded, "no slice was unusable")
	assert.Equal(t, []string{data}, res.RepairedFiles)

	info, err := os.Stat(data)
	require.NoError(t, err)
	assert.Equal(t, int64(size), info.Size(), "the trailing garbage must be gone")
	assertVerifiedOK(t, data+".par2")
}

func TestRepairStream_MultiFileSet(t *testing.T) {
	dir := t.TempDir()
	// one set over two files; only the first gets damaged
	parPath := filepath.Join(dir, "set.par2")
	a := writeInputFile(t, dir, "a.bin", 3*testSlice, 5)
	b := writeInputFile(t, dir, "b.bin", 2*testSlice, 6)
	require.NoError(t, CreateStream(context.Background(), parPath, []string{a, b}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
	}))

	corruptAt(t, a, testSlice)

	res, err := RepairStream(context.Background(), parPath, RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{a}, res.RepairedFiles, "only the damaged file may be rewritten")

	originalA := randomBytes(3*testSlice, 5)
	repaired, err := os.ReadFile(a)
	require.NoError(t, err)
	assert.Equal(t, originalA, repaired)
	assertVerifiedOK(t, parPath)
}

func TestRepairStream_NoDamageIsNoop(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 7)

	res, err := RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assert.False(t, res.RepairNeeded)
	assert.Empty(t, res.RepairedFiles, "an intact set must not be rewritten")
}

func TestRepairStream_NotEnoughParity(t *testing.T) {
	dir := t.TempDir()
	const size = 100_000
	data := writeFixture(t, dir, "data.bin", size, 8)

	// four damaged slices against three parity shards
	for i := range 4 {
		corruptAt(t, data, int64(i)*testSlice)
	}
	damaged, err := os.ReadFile(data)
	require.NoError(t, err)

	_, err = RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.ErrorIs(t, err, ErrNotEnoughParity)

	after, err := os.ReadFile(data)
	require.NoError(t, err)
	assert.Equal(t, damaged, after, "a failed repair must not touch the files")
}

func TestRepairStream_NoVolumes(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 9)
	corruptAt(t, data, 0)

	volumes, err := filepath.Glob(filepath.Join(dir, "*.vol*.par2"))
	require.NoError(t, err)
	require.NotEmpty(t, volumes)
	for _, v := range volumes {
		require.NoError(t, os.Remove(v))
	}

	_, err = RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.ErrorIs(t, err, ErrNotEnoughParity)
}

func TestRepairStream_CorruptVolume(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 10)
	corruptAt(t, data, 0)

	// a volume that fails packet-hash validation aborts the repair — the
	// shard bytes would not be the ones the set was verified against
	volumes, err := filepath.Glob(filepath.Join(dir, "*.vol*.par2"))
	require.NoError(t, err)
	require.NotEmpty(t, volumes)
	corruptAt(t, volumes[0], 100)

	_, err = RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.Error(t, err, "a corrupt volume must abort the repair")
}

func TestRepairStream_Cancelled(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 11)
	corruptAt(t, data, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RepairStream(ctx, data+".par2", RepairOptions{NumGoroutines: 1})
	require.ErrorIs(t, err, context.Canceled)
}

// TestRepairStream_CreateStreamParity pins that the parity CreateStream
// writes repairs too — not just gopar-generated sets.
func TestRepairStream_CreateStreamParity(t *testing.T) {
	dir := t.TempDir()
	data := writeInputFile(t, dir, "data.bin", 3*testSlice+5, 12)
	original, err := os.ReadFile(data)
	require.NoError(t, err)
	require.NoError(t, CreateStream(context.Background(), data+".par2", []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
	}))

	corruptAt(t, data, testSlice+1)

	_, err = RepairStream(context.Background(), data+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)

	repaired, err := os.ReadFile(data)
	require.NoError(t, err)
	assert.Equal(t, original, repaired)
}

// TestRepairStream_EqGoparRepair is the cross-oracle: the same damaged
// corpus, repaired by gopar and by RepairStream, must come out identical.
func TestRepairStream_EqGoparRepair(t *testing.T) {
	if testing.Short() {
		t.Skip("the oracle needs gopar")
	}

	build := func(t *testing.T) (string, []byte) {
		t.Helper()
		dir := t.TempDir()
		const size = 100_000
		data := writeFixture(t, dir, "data.bin", size, 13)
		original, err := os.ReadFile(data)
		require.NoError(t, err)
		corruptAt(t, data, int64(size/3))
		return data, original
	}

	oursData, original := build(t)
	_, err := RepairStream(context.Background(), oursData+".par2", RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)

	goparData, _ := build(t)
	_, err = gopar.Repair(goparData+".par2", gopar.RepairOptions{NumGoroutines: 1, DoubleCheck: true})
	require.NoError(t, err, "gopar must repair the same corpus")

	ours, err := os.ReadFile(oursData)
	require.NoError(t, err)
	theirs, err := os.ReadFile(goparData)
	require.NoError(t, err)
	assert.Equal(t, ours, theirs, "both repairs must restore the same bytes")
	assert.Equal(t, original, ours, "and the bytes must be the original content")
}

// TestRepairStream_Progress pins the delegate contract: the scan phase
// reports slice progress, the write phase reports rewritten files.
func TestRepairStream_Progress(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 14)
	corruptAt(t, data, 0)

	var scanDone, scanTotal, writeDone, writeTotal int
	res, err := RepairStream(context.Background(), data+".par2", RepairOptions{
		NumGoroutines: 1,
		Delegate: probeDelegate{func(done, total int) { scanDone, scanTotal = done, total },
			func(done, total int) { writeDone, writeTotal = done, total }},
	})
	require.NoError(t, err)
	require.Len(t, res.RepairedFiles, 1)

	assert.Equal(t, 2, scanTotal, "the scan must report every slice of the set")
	assert.Equal(t, 1, scanDone, "the corrupt slice never resolves")
	assert.Equal(t, 1, writeTotal)
	assert.Equal(t, 1, writeDone)
}

type probeDelegate struct {
	onScan func(done, total int)
	onWrite func(done, total int)
}

func (d probeDelegate) OnScanProgress(done, total int)  { d.onScan(done, total) }
func (d probeDelegate) OnWriteProgress(done, total int) { d.onWrite(done, total) }

// TestRepairStream_LocationMap checks the cross-file relocation: the bytes
// of every expected slice must survive the repair even when the scan found
// them in a different file. The corpus holds two files with one identical
// shared slice.
func TestRepairStream_RepairKeepsSetReadable(t *testing.T) {
	dir := t.TempDir()
	parPath := filepath.Join(dir, "set.par2")
	a := writeInputFile(t, dir, "a.bin", 2*testSlice, 15)
	b := writeInputFile(t, dir, "b.bin", 2*testSlice, 16)
	require.NoError(t, CreateStream(context.Background(), parPath, []string{a, b}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
	}))

	// both files damaged at once: the reconstruction needs shards of both
	corruptAt(t, a, 0)
	corruptAt(t, b, testSlice)

	_, err := RepairStream(context.Background(), parPath, RepairOptions{NumGoroutines: 1})
	require.NoError(t, err)
	assertVerifiedOK(t, parPath)
	assert.Equal(t, randomBytes(2*testSlice, 15), mustRead(t, a))
	assert.Equal(t, randomBytes(2*testSlice, 16), mustRead(t, b))
}

func mustRead(t testing.TB, p string) []byte {
	t.Helper()
	bs, err := os.ReadFile(p)
	require.NoError(t, err)
	return bs
}

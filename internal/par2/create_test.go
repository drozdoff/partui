package par2

// Tests of the streaming create (plans/streaming_file.md §6.7): byte-golden
// output against gopar.Create, the error paths (an empty data file must be
// an error, not the gopar panic), cancellation, and the cross-oracle —
// gopar must be able to verify and repair what CreateStream wrote.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gopar "github.com/akalin/gopar/par2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenCorpus is the size table of §6.7: tiny sizes, the slice boundaries,
// and the CalculateBlockSize branches of the blockCountLimits rows.
var goldenCorpus = []struct {
	size  int
	slice int
}{
	{1, 4096},
	{4, 4096},
	{testSlice - 1, testSlice},
	{testSlice, testSlice},
	{testSlice + 1, testSlice},
	{100_000, testSlice},
	{5_000_000, testSlice},
	// one corpus per blockCountLimits row, sliced like the app does
	{1_000_000, CalculateBlockSize(1_000_000, 5)},
	{4_000_000, CalculateBlockSize(4_000_000, 5)},
	{20_000_000, CalculateBlockSize(20_000_000, 5)},
}

// writeInputFile writes the deterministic bytes of one corpus member.
func writeInputFile(t testing.TB, dir, name string, size int, seed int64) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, randomBytes(size, seed), 0o644))
	return p
}

// createWithGopar runs gopar.Create with fixed shard counts.
func createWithGopar(t testing.TB, parPath string, files []string, slice int) {
	t.Helper()
	require.NoError(t, gopar.Create(parPath, files, gopar.CreateOptions{
		SliceByteCount:  slice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))
}

// assertSetsEqual demands byte-identical PAR2 sets: same file names, same
// bytes in every file.
func assertSetsEqual(t *testing.T, wantDir, gotDir string) {
	t.Helper()

	entries, err := os.ReadDir(wantDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	gotEntries, err := os.ReadDir(gotDir)
	require.NoError(t, err)
	var gotNames []string
	for _, e := range gotEntries {
		gotNames = append(gotNames, e.Name())
	}
	assert.Equal(t, names, gotNames, "the sets must contain the same files")

	for _, name := range names {
		want, err := os.ReadFile(filepath.Join(wantDir, name))
		require.NoError(t, err)
		got, err := os.ReadFile(filepath.Join(gotDir, name))
		require.NoError(t, err)
		require.Equal(t, want, got, "%s must be byte-identical to gopar's output", name)
	}
}

// TestCreateStream_GoldenBytes pins the output byte-for-byte against
// gopar.Create on the whole corpus.
func TestCreateStream_GoldenBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("golden corpus is generated with gopar")
	}

	for _, tc := range goldenCorpus {
		t.Run(fmt.Sprintf("size=%d/slice=%d", tc.size, tc.slice), func(t *testing.T) {
			if testing.Short() && tc.size > 100_000 {
				t.Skip("large corpus")
			}

			gDir, sDir := t.TempDir(), t.TempDir()
			gData := writeInputFile(t, gDir, "data.bin", tc.size, int64(tc.size))
			sData := writeInputFile(t, sDir, "data.bin", tc.size, int64(tc.size))

			createWithGopar(t, gData+".par2", []string{gData}, tc.slice)
			require.NoError(t, CreateStream(context.Background(), sData+".par2", []string{sData}, CreateOptions{
				SliceByteCount:  tc.slice,
				NumParityShards: -1,
				NumGoroutines:   1,
			}))

			assertSetsEqual(t, gDir, sDir)
		})
	}
}

// NumGoroutines must not influence the bytes: the default goroutine count
// has to land on gopar's output too.
func TestCreateStream_GoldenBytes_GoroutineDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("golden corpus is generated with gopar")
	}

	gDir, sDir := t.TempDir(), t.TempDir()
	gData := writeInputFile(t, gDir, "data.bin", 100_000, 1)
	sData := writeInputFile(t, sDir, "data.bin", 100_000, 1)

	createWithGopar(t, gData+".par2", []string{gData}, testSlice)
	require.NoError(t, CreateStream(context.Background(), sData+".par2", []string{sData}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   0, // the default, unlike the fixed 1 above
	}))

	assertSetsEqual(t, gDir, sDir)
}

// Multi-file sets exercise the recovery-set sort and the global slice index
// across files; the duplicate entry covers the degenerate same-file-twice
// input, whose equal file IDs must not destabilize the output.
func TestCreateStream_GoldenBytes_MultiFile(t *testing.T) {
	if testing.Short() {
		t.Skip("golden corpus is generated with gopar")
	}

	files := []struct {
		name string
		size int
	}{
		{"a.bin", 37_000},
		{"b.bin", 100_000},
		{"c.bin", testSlice / 2},
		{"a.bin", 37_000}, // duplicate: same file, same file ID
	}

	gDir, sDir := t.TempDir(), t.TempDir()
	var gFiles, sFiles []string
	for i, f := range files {
		gFiles = append(gFiles, writeInputFile(t, gDir, f.name, f.size, int64(i+1)))
		sFiles = append(sFiles, writeInputFile(t, sDir, f.name, f.size, int64(i+1)))
	}

	createWithGopar(t, filepath.Join(gDir, "set.par2"), gFiles, testSlice)
	require.NoError(t, CreateStream(context.Background(), filepath.Join(sDir, "set.par2"), sFiles, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
	}))

	assertSetsEqual(t, gDir, sDir)
}

// TestCreateStream_Errors pins the input validation: every case must be an
// error with no output written — in particular the empty data file, which
// panics in gopar's RS coder (plans/streaming_file.md §6.6.3).
func TestCreateStream_Errors(t *testing.T) {
	dir := t.TempDir()

	t.Run("bad extension", func(t *testing.T) {
		data := writeInputFile(t, dir, "x.bin", 100, 1)
		err := CreateStream(context.Background(), data+".par", []string{data}, CreateOptions{})
		assert.ErrorContains(t, err, ".par2 extension")
	})

	t.Run("no files", func(t *testing.T) {
		err := CreateStream(context.Background(), filepath.Join(dir, "y.par2"), nil, CreateOptions{})
		assert.ErrorContains(t, err, "must not be empty")
	})

	t.Run("empty data file", func(t *testing.T) {
		data := writeInputFile(t, dir, "empty.bin", 0, 1)
		err := CreateStream(context.Background(), data+".par2", []string{data}, CreateOptions{SliceByteCount: testSlice})
		require.ErrorContains(t, err, "empty data file")
		_, statErr := os.Stat(data + ".par2")
		assert.True(t, os.IsNotExist(statErr), "no output may be written")
	})

	t.Run("file outside base dir", func(t *testing.T) {
		other := t.TempDir()
		data := writeInputFile(t, other, "z.bin", 100, 1)
		err := CreateStream(context.Background(), filepath.Join(dir, "z.par2"), []string{data}, CreateOptions{})
		assert.ErrorContains(t, err, "must lie in basePath")
	})

	t.Run("invalid slice byte count", func(t *testing.T) {
		data := writeInputFile(t, dir, "s.bin", 100, 1)
		err := CreateStream(context.Background(), data+".par2", []string{data}, CreateOptions{SliceByteCount: 6})
		assert.ErrorContains(t, err, "invalid slice byte count")
	})
}

// TestCreateStream_Cancel pins §6.6.2: a cancellation mid-scan surfaces as
// context.Canceled and leaves no output behind.
func TestCreateStream_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	// 25 slices; the delegate pulls the plug after a few
	data := writeInputFile(t, dir, "data.bin", 100_000, 1)

	var reports int
	err := CreateStream(ctx, data+".par2", []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
		Delegate: progressFunc(func(done, total int) {
			reports++
			if done == 5 {
				cancel()
			}
		}),
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, reports, 5, "progress must have been reported before the cancel")

	matches, err := filepath.Glob(filepath.Join(dir, "data.bin*.par2"))
	require.NoError(t, err)
	assert.Empty(t, matches, "a cancelled create must leave no output")
}

type progressFunc func(done, total int)

func (f progressFunc) OnProgress(done, total int) { f(done, total) }

// A failure in the middle of the write phase must remove the files written
// so far: here the first volume path is pre-taken by a directory, so the
// index has already been written when the volume open fails.
func TestCreateStream_WriteErrorCleanup(t *testing.T) {
	dir := t.TempDir()
	data := writeInputFile(t, dir, "data.bin", 2*testSlice, 1)

	// base of data.bin.par2 is data.bin: the first volume is
	// data.bin.vol00+01.par2 (%02d of 0 is "00", like in gopar) — a
	// directory there breaks the write phase
	require.NoError(t, os.Mkdir(data+".vol00+01.par2", 0o755))

	err := CreateStream(context.Background(), data+".par2", []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
	})
	require.Error(t, err)

	_, statErr := os.Stat(data + ".par2")
	assert.True(t, os.IsNotExist(statErr), "the partial index must be removed")
	info, statErr := os.Stat(data + ".vol00+01.par2")
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "the pre-existing directory must survive the cleanup")
}

// TestCreateThenGoparVerify is the strongest cross-check of §6.7: gopar
// must verify every set CreateStream writes as fully healthy.
func TestCreateThenGoparVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus is generated with gopar")
	}

	for _, tc := range goldenCorpus {
		t.Run(fmt.Sprintf("size=%d/slice=%d", tc.size, tc.slice), func(t *testing.T) {
			if testing.Short() || tc.size > 5_000_000 {
				t.Skip("covered by the golden test run")
			}

			dir := t.TempDir()
			data := writeInputFile(t, dir, "data.bin", tc.size, int64(tc.size))
			require.NoError(t, CreateStream(context.Background(), data+".par2", []string{data}, CreateOptions{
				SliceByteCount:  tc.slice,
				NumParityShards: -1,
				NumGoroutines:   1,
			}))

			res, err := gopar.Verify(data+".par2", gopar.VerifyOptions{
				NumGoroutines:  1,
				VerifyDelegate: gopar.DoNothingVerifyDelegate{},
			})
			require.NoError(t, err, "gopar must verify a CreateStream output")
			assert.False(t, res.ShardCounts.RepairNeeded())
			assert.Zero(t, res.ShardCounts.UnusableDataShardCount,
				"every slice must be usable")
		})
	}
}

// TestRepairViaGopar proves the parity bytes themselves are right: gopar
// must restore the original content of a corrupted file from nothing but
// the set CreateStream wrote (plans/streaming_file.md §6.7).
func TestRepairViaGopar(t *testing.T) {
	if testing.Short() {
		t.Skip("repair corpus is generated with gopar")
	}

	dir := t.TempDir()
	const size = 100_000 // 25 slices at testSlice; 3 shards repair one
	data := writeInputFile(t, dir, "data.bin", size, 1)
	original, err := os.ReadFile(data)
	require.NoError(t, err)

	require.NoError(t, CreateStream(context.Background(), data+".par2", []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
	}))

	// corrupt the middle slice of a copy of the corpus
	dirCopy := t.TempDir()
	for _, name := range []string{"data.bin", "data.bin.par2", "data.bin.vol00+01.par2", "data.bin.vol01+02.par2"} {
		bs, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dirCopy, name), bs, 0o644))
	}
	corrupted := filepath.Join(dirCopy, "data.bin")
	raw, err := os.ReadFile(corrupted)
	require.NoError(t, err)
	raw[size/2] ^= 0xFF
	require.NoError(t, os.WriteFile(corrupted, raw, 0o644))

	_, err = gopar.Repair(corrupted+".par2", gopar.RepairOptions{
		NumGoroutines: 1,
		DoubleCheck:   true,
	})
	require.NoError(t, err, "gopar must repair from CreateStream parity")

	repaired, err := os.ReadFile(corrupted)
	require.NoError(t, err)
	assert.Equal(t, original, repaired, "the repair must restore the original content")
}

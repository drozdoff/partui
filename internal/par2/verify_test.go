package par2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	gopar "github.com/akalin/gopar/par2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSlice is the slice size of all fixtures; small enough for fast RS
// math, big enough to exercise padding and window shifts.
const testSlice = 4096

func randomBytes(size int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	bs := make([]byte, size)
	rng.Read(bs)
	return bs
}

// writeFixture writes a deterministic data file and creates its PAR2 set
// with gopar (the test generator/oracle), one data file per set — the shape
// partui always creates. Returns the data path.
func writeFixture(t testing.TB, dir, name string, size int, seed int64) string {
	t.Helper()
	data := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(data, randomBytes(size, seed), 0o644))
	require.NoError(t, gopar.Create(data+".par2", []string{data}, gopar.CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))
	return data
}

func expectedSliceCount(size int) int {
	return (size + testSlice - 1) / testSlice
}

func TestParseParSet_Golden(t *testing.T) {
	dir := t.TempDir()
	const size = 3 * testSlice // exact slices, no padding
	data := writeFixture(t, dir, "data.bin", size, 1)

	meta, err := parseParSet(data + ".par2")
	require.NoError(t, err)

	assert.Equal(t, testSlice, meta.sliceByteCount)
	assert.NotEqual(t, recoverySetID{}, meta.setID)
	assert.Equal(t, "gopar", meta.clientID)
	require.Len(t, meta.recoverySet, 1)

	desc, ok := meta.descriptions[meta.recoverySet[0]]
	require.True(t, ok, "the recovery set file must have a description")
	assert.Equal(t, "data.bin", desc.filename)
	assert.Equal(t, size, desc.byteCount)
	assert.Equal(t, md5.Sum(randomBytes(size, 1)), desc.hash)

	pairs := meta.ifsc[meta.recoverySet[0]]
	require.Len(t, pairs, expectedSliceCount(size))

	require.NoError(t, meta.loadParity(data+".par2"))
	assert.Equal(t, 3, meta.parityExponents, "gopar with NumParityShards=-1 writes 3 shards")
}

func TestParseParSet_MultiFile(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeFixture(t, dir, "a.bin", 2*testSlice, 2),
		writeFixture(t, dir, "b.bin", testSlice, 3),
	}
	// one set over both files
	parPath := filepath.Join(dir, "multi.par2")
	require.NoError(t, gopar.Create(parPath, files, gopar.CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))

	meta, err := parseParSet(parPath)
	require.NoError(t, err)
	require.Len(t, meta.recoverySet, 2)

	var names []string
	for _, id := range meta.recoverySet {
		names = append(names, meta.descriptions[id].filename)
	}
	assert.ElementsMatch(t, []string{"a.bin", "b.bin"}, names)
}

// craftDescriptionBody builds a file description packet body for the given
// file parameters.
func craftDescriptionBody(id fileID, hash, sixteenK [md5.Size]byte, size uint64, name string) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, fileDescriptionPacketHeader{
		FileID:       id,
		Hash:         hash,
		SixteenKHash: sixteenK,
		Length:       size,
	})
	buf.WriteString(name)
	return buf.Bytes()
}

func TestParseParSet_Errors(t *testing.T) {
	someID := fileID{1}
	someHash := [md5.Size]byte{2}
	some16K := [md5.Size]byte{3}

	// build an index file from crafted packets; the set ID is adopted from
	// the first packet, so any nonzero value works
	build := func(packets ...[]byte) string {
		path := filepath.Join(t.TempDir(), "index.par2")
		var buf bytes.Buffer
		for _, p := range packets {
			buf.Write(p)
		}
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
		return path
	}
	set := recoverySetID{5}
	creator := func() []byte { return craftPacket(set, creatorPacketType, []byte("gopar"), true) }

	tests := []struct {
		name    string
		parPath string
		wantErr string
	}{
		{
			name:    "no main packet",
			parPath: build(creator()),
			wantErr: "no main packet found",
		},
		{
			name: "no file description",
			parPath: build(
				creator(),
				craftPacket(set, mainPacketType, craftMainBody(testSlice, []fileID{someID}, nil), true),
			),
			wantErr: "file description packet not found",
		},
		{
			name: "no IFSC packet",
			parPath: build(
				creator(),
				craftPacket(set, mainPacketType, craftMainBody(testSlice, []fileID{computeFileID(some16K, 100, []byte("x.bin"))}, nil), true),
				craftPacket(set, fileDescriptionPacketType,
					craftDescriptionBody(computeFileID(some16K, 100, []byte("x.bin")), someHash, some16K, 100, "x.bin"), true),
			),
			wantErr: "input file slice checksum packet not found",
		},
		{
			name: "recovery packets in index",
			parPath: build(
				creator(),
				craftPacket(set, mainPacketType, craftMainBody(testSlice, []fileID{someID}, nil), true),
				craftPacket(set, recoveryPacketType, []byte{1, 0, 0, 0, 1, 1, 1, 1}, true),
			),
			wantErr: "recovery packets found in index file",
		},
		{
			name:    "no packets at all",
			parPath: build(),
			wantErr: "no packets found",
		},
		{
			name:    "garbage bytes",
			parPath: build(bytes.Repeat([]byte{'x'}, 200)),
			wantErr: "unexpected magic string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseParSet(tc.parPath)
			require.Error(t, err)
			assert.EqualError(t, err, tc.wantErr)
		})
	}

	t.Run("zero-length file description rejected", func(t *testing.T) {
		path := build(
			creator(),
			craftPacket(set, mainPacketType, craftMainBody(testSlice, []fileID{someID}, nil), true),
			craftPacket(set, fileDescriptionPacketType,
				craftDescriptionBody(computeFileID(some16K, 0, []byte("x.bin")), someHash, some16K, 0, "x.bin"), true),
		)
		_, err := parseParSet(path)
		require.Error(t, err)
		assert.EqualError(t, err, "empty files not allowed")
	})
}

func TestLoadParity_Errors(t *testing.T) {
	t.Run("valid volumes", func(t *testing.T) {
		dir := t.TempDir()
		data := writeFixture(t, dir, "data.bin", testSlice, 4)
		meta, err := parseParSet(data + ".par2")
		require.NoError(t, err)
		require.NoError(t, meta.loadParity(data+".par2"))
		assert.Equal(t, 3, meta.parityExponents)
	})

	t.Run("slice byte count mismatch", func(t *testing.T) {
		dir := t.TempDir()
		data := writeFixture(t, dir, "data.bin", testSlice, 5)
		meta, err := parseParSet(data + ".par2")
		require.NoError(t, err)

		body := craftMainBody(uint64(testSlice*2), meta.recoverySet, nil)
		vol := filepath.Join(dir, "data.bin.vol99+00.par2")
		var buf bytes.Buffer
		buf.Write(craftPacket(meta.setID, creatorPacketType, []byte("gopar"), true))
		buf.Write(craftPacket(meta.setID, mainPacketType, body, true))
		require.NoError(t, os.WriteFile(vol, buf.Bytes(), 0o644))

		err = meta.loadParity(data + ".par2")
		require.Error(t, err)
		assert.EqualError(t, err, "slice byte count mismatch")
	})

	t.Run("recovery set mismatch", func(t *testing.T) {
		dir := t.TempDir()
		data := writeFixture(t, dir, "data.bin", testSlice, 6)
		meta, err := parseParSet(data + ".par2")
		require.NoError(t, err)

		foreign := fileID{0xAA}
		require.True(t, fileIDLess(foreign, meta.recoverySet[0]) || fileIDLess(meta.recoverySet[0], foreign))
		body := craftMainBody(uint64(testSlice), []fileID{foreign}, nil)
		vol := filepath.Join(dir, "data.bin.vol99+00.par2")
		var buf bytes.Buffer
		buf.Write(craftPacket(meta.setID, creatorPacketType, []byte("gopar"), true))
		buf.Write(craftPacket(meta.setID, mainPacketType, body, true))
		require.NoError(t, os.WriteFile(vol, buf.Bytes(), 0o644))

		err = meta.loadParity(data + ".par2")
		require.Error(t, err)
		assert.EqualError(t, err, "recovery set mismatch")
	})

	t.Run("volume of a foreign set is skipped", func(t *testing.T) {
		dir := t.TempDir()
		data := writeFixture(t, dir, "data.bin", testSlice, 7)
		meta, err := parseParSet(data + ".par2")
		require.NoError(t, err)

		foreignSet := recoverySetID{0xEE}
		vol := filepath.Join(dir, "data.bin.vol98+00.par2")
		var buf bytes.Buffer
		buf.Write(craftPacket(foreignSet, creatorPacketType, []byte("other"), true))
		buf.Write(craftPacket(foreignSet, mainPacketType, craftMainBody(uint64(testSlice), []fileID{{0xCC}}, nil), true))
		require.NoError(t, os.WriteFile(vol, buf.Bytes(), 0o644))

		require.NoError(t, meta.loadParity(data+".par2"))
		assert.Equal(t, 3, meta.parityExponents, "the foreign volume must not change the parity count")
	})

	t.Run("volume without a creator packet", func(t *testing.T) {
		dir := t.TempDir()
		data := writeFixture(t, dir, "data.bin", testSlice, 8)
		meta, err := parseParSet(data + ".par2")
		require.NoError(t, err)

		var buf bytes.Buffer
		buf.Write(craftPacket(meta.setID, mainPacketType, craftMainBody(uint64(testSlice), meta.recoverySet, nil), true))
		buf.Write(craftPacket(meta.setID, recoveryPacketType, []byte{5, 0, 0, 0, 1, 2, 3, 4}, true))
		vol := filepath.Join(dir, "data.bin.vol97+00.par2")
		require.NoError(t, os.WriteFile(vol, buf.Bytes(), 0o644))

		err = meta.loadParity(data + ".par2")
		require.Error(t, err)
		assert.EqualError(t, err, "no creator packet found")
	})
}

func TestVerifyStream_Healthy(t *testing.T) {
	// sizes below, at and above the sixteenK boundary pin the streaming
	// file-hash and sixteenK computation
	for _, size := range []int{1, 4, testSlice - 1, testSlice, testSlice + 1, 16_000, 16_384, 16_385, 3*testSlice + 7} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			dir := t.TempDir()
			data := writeFixture(t, dir, "data.bin", size, int64(size))
			results, err := VerifyStream(context.Background(), data+".par2", nil)
			require.NoError(t, err)
			require.Len(t, results, 1)

			res := results[0]
			assert.Equal(t, data, res.Path)
			assert.False(t, res.Missing)
			assert.False(t, res.HashMismatch, "a freshly created archive must match its hashes")
			assert.False(t, res.WrongByteCount)
			assert.Equal(t, expectedSliceCount(size), res.UsableShards)
			assert.Zero(t, res.UnusableShards)
			assert.False(t, res.RepairNeeded)
		})
	}
}

func TestVerifyStream_CorruptByte(t *testing.T) {
	dir := t.TempDir()
	const size = 20 * testSlice
	data := writeFixture(t, dir, "data.bin", size, 9)

	t.Run("beyond sixteenK", func(t *testing.T) {
		raw, err := os.ReadFile(data)
		require.NoError(t, err)
		raw[15_000] ^= 0xFF // inside the first slice, outside the first 16 KiB
		require.NoError(t, os.WriteFile(data, raw, 0o644))

		results, err := VerifyStream(context.Background(), data+".par2", nil)
		require.NoError(t, err)
		require.Len(t, results, 1)

		res := results[0]
		assert.True(t, res.HashMismatch)
		assert.False(t, res.WrongByteCount, "the size did not change")
		assert.True(t, res.RepairNeeded)
		assert.Equal(t, expectedSliceCount(size)-1, res.UsableShards)
		assert.Equal(t, 1, res.UnusableShards)
	})

	t.Run("inside sixteenK", func(t *testing.T) {
		raw, err := os.ReadFile(data)
		require.NoError(t, err)
		raw[100] ^= 0xFF
		require.NoError(t, os.WriteFile(data, raw, 0o644))

		results, err := VerifyStream(context.Background(), data+".par2", nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.True(t, results[0].HashMismatch, "the sixteenK hash must catch it")
		assert.True(t, results[0].RepairNeeded)
	})
}

// gopar quirk (plans/streaming_file.md §5.2.6): a data shift lets every
// checksum pair be found somewhere, so the file counts as repairable-not-
// needed even though its hashes no longer match. Deliberately reproduced.
func TestVerifyStream_InsertByteQuirk(t *testing.T) {
	dir := t.TempDir()
	const size = 4 * testSlice
	data := writeFixture(t, dir, "data.bin", size, 10)

	raw, err := os.ReadFile(data)
	require.NoError(t, err)
	shifted := append([]byte{'Q'}, raw...)
	require.NoError(t, os.WriteFile(data, shifted, 0o644))

	results, err := VerifyStream(context.Background(), data+".par2", nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	res := results[0]
	assert.True(t, res.HashMismatch)
	assert.True(t, res.WrongByteCount)
	assert.False(t, res.RepairNeeded, "all pairs are found at the shifted positions")
	assert.Equal(t, expectedSliceCount(size), res.UsableShards)
}

func TestVerifyStream_MissingDataFile(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 11)
	require.NoError(t, os.Remove(data))

	results, err := VerifyStream(context.Background(), data+".par2", nil)
	require.NoError(t, err, "a missing data file is a status, not an error")
	require.Len(t, results, 1)

	res := results[0]
	assert.True(t, res.Missing)
	assert.True(t, res.RepairNeeded)
	assert.Zero(t, res.UsableShards)
	assert.Equal(t, expectedSliceCount(2*testSlice), res.UnusableShards)
}

func TestVerifyStream_BadInputs(t *testing.T) {
	t.Run("wrong extension", func(t *testing.T) {
		_, err := VerifyStream(context.Background(), filepath.Join(t.TempDir(), "data.bin"), nil)
		assert.EqualError(t, err, "parPath must have a .par2 extension")
	})

	t.Run("missing index", func(t *testing.T) {
		_, err := VerifyStream(context.Background(), filepath.Join(t.TempDir(), "nope.par2"), nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, os.ErrNotExist))
	})

	t.Run("garbage index", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "garbage.par2")
		require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{0}, 100), 0o644))
		_, err := VerifyStream(context.Background(), path, nil)
		require.Error(t, err)
	})
}

func TestVerifyStream_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	data := writeFixture(t, dir, "data.bin", 2*testSlice, 12)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := VerifyStream(ctx, data+".par2", nil)
	assert.ErrorIs(t, err, context.Canceled)
}

// cancelling mid-scan: the delegate cancels as soon as the first file made
// progress, the second file must not be scanned (two-file set via gopar).
func TestVerifyStream_CancelMidScan(t *testing.T) {
	dir := t.TempDir()
	a := writeFixture(t, dir, "a.bin", 8*testSlice, 13)
	b := writeFixture(t, dir, "b.bin", 8*testSlice, 14)
	parPath := filepath.Join(dir, "set.par2")
	require.NoError(t, gopar.Create(parPath, []string{a, b}, gopar.CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dg := cancelOnFirstProgress{cancel: cancel}
	_, err := VerifyStream(ctx, parPath, dg)
	assert.ErrorIs(t, err, context.Canceled)
}

// cancelOnFirstProgress cancels on the first non-empty progress report;
// CancelFunc is idempotent, so no extra synchronisation is needed.
type cancelOnFirstProgress struct {
	cancel context.CancelFunc
}

func (c cancelOnFirstProgress) OnProgress(done, total int) {
	if done > 0 {
		c.cancel()
	}
}

// the progress delegate sees a monotonic counter reaching the total
func TestVerifyStream_Progress(t *testing.T) {
	dir := t.TempDir()
	const size = 5 * testSlice
	data := writeFixture(t, dir, "data.bin", size, 15)

	var mu sync.Mutex
	var reports [][2]int
	dg := collectDelegate{fn: func(done, total int) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, [2]int{done, total})
	}}

	results, err := VerifyStream(context.Background(), data+".par2", dg)
	require.NoError(t, err)
	require.Len(t, results, 1)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, reports)
	total := expectedSliceCount(size)
	last := reports[len(reports)-1]
	assert.Equal(t, [2]int{total, total}, last, "the last report must be complete")
	for i, r := range reports {
		assert.LessOrEqual(t, r[0], r[1])
		if i > 0 {
			assert.GreaterOrEqual(t, r[0], reports[i-1][0], "progress must be monotonic")
		}
	}
}

type collectDelegate struct {
	fn func(done, total int)
}

func (d collectDelegate) OnProgress(done, total int) { d.fn(done, total) }

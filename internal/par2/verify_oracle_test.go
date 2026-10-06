package par2

// Oracle tests: the streaming verify must produce exactly the same statuses
// and shard counts as gopar.Verify on the same corpus (plans/streaming_file.md
// §5.2, §5.6). The corpus is generated with gopar itself in the test — no
// binary fixtures in the repository.

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

// writeCorpus writes a deterministic data file and creates its PAR2 set with
// an explicit slice size.
func writeCorpus(t *testing.T, dir string, size, slice int) string {
	t.Helper()
	data := filepath.Join(dir, "data.bin")
	require.NoError(t, os.WriteFile(data, randomBytes(size, int64(size)), 0o644))
	require.NoError(t, gopar.Create(data+".par2", []string{data}, gopar.CreateOptions{
		SliceByteCount:  slice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))
	return data
}

// copySet copies a whole PAR2 set (data, index, volumes) into a fresh
// directory, so mutations never leak between cases.
func copySet(t *testing.T, srcDir string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(srcDir)
	require.NoError(t, err)
	for _, e := range entries {
		bs, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, e.Name()), bs, 0o644))
	}
	return dst
}

func flipByteAt(offset int) func(*testing.T, string, int) {
	return func(t *testing.T, dataPath string, size int) {
		t.Helper()
		raw, err := os.ReadFile(dataPath)
		require.NoError(t, err)
		raw[offset] ^= 0xFF
		require.NoError(t, os.WriteFile(dataPath, raw, 0o644))
	}
}

func truncateTo(newSize func(int) int) func(*testing.T, string, int) {
	return func(t *testing.T, dataPath string, size int) {
		t.Helper()
		raw, err := os.ReadFile(dataPath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(dataPath, raw[:newSize(len(raw))], 0o644))
	}
}

var oracleMutations = []struct {
	name  string
	apply func(t *testing.T, dataPath string, size int)
}{
	{"none", nil},
	{"flip-first", flipByteAt(0)},
	{"flip-mid", func(t *testing.T, p string, size int) { flipByteAt(size/2)(t, p, size) }},
	{"flip-last", func(t *testing.T, p string, size int) { flipByteAt(size-1)(t, p, size) }},
	{"truncate-1", truncateTo(func(size int) int { return size - 1 })},
	{"truncate-half", truncateTo(func(size int) int { return size / 2 })},
	{"truncate-0", truncateTo(func(int) int { return 0 })},
	{"append-1", func(t *testing.T, p string, size int) {
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, append(raw, 'Q'), 0o644))
	}},
	{"remove-data", func(t *testing.T, p string, _ int) { require.NoError(t, os.Remove(p)) }},
}

// compareWithGopar runs both implementations over the same set and demands
// identical outcomes: an error on both sides, or identical usable/unusable
// shard counts and repair verdict.
func compareWithGopar(t *testing.T, parPath string) {
	t.Helper()

	gRes, gErr := gopar.Verify(parPath, gopar.VerifyOptions{
		NumGoroutines:  1,
		VerifyDelegate: gopar.DoNothingVerifyDelegate{},
	})
	results, err := VerifyStream(context.Background(), parPath, nil)

	if gErr != nil {
		require.Error(t, err, "gopar errored (%v), VerifyStream must error too", gErr)
		return
	}
	require.NoError(t, err)

	var usable, unusable int
	for _, r := range results {
		usable += r.UsableShards
		unusable += r.UnusableShards
		assert.Equal(t, r.UnusableShards > 0, r.RepairNeeded, "RepairNeeded must follow the unusable count")
	}
	assert.Equal(t, gRes.ShardCounts.UsableDataShardCount, usable, "usable shards")
	assert.Equal(t, gRes.ShardCounts.UnusableDataShardCount, unusable, "unusable shards")
	assert.Equal(t, gRes.ShardCounts.RepairNeeded(), unusable > 0, "repair verdict")
}

// TestVerifyStreamOracle is the mutation table of §5.6: one corpus per row,
// every mutation applied to a fresh copy, every cell cross-checked against
// gopar.
func TestVerifyStreamOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("oracle corpus is generated with gopar")
	}

	cases := []struct {
		size  int
		slice int
	}{
		{1, 4096},
		{4, 4096},
		{100_000, 4096},
		{4095, 4096},
		{4096, 4096},
		{4097, 4096},
		{5_000_000, 4096},
		// a different slice size, the CalculateBlockSize branch of a 1 MB file
		{1_000_000, CalculateBlockSize(1_000_000, 5)},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("size=%d/slice=%d", tc.size, tc.slice), func(t *testing.T) {
			if testing.Short() {
				t.Skip("oracle corpus is generated with gopar")
			}
			srcDir := t.TempDir()
			writeCorpus(t, srcDir, tc.size, tc.slice)

			for _, m := range oracleMutations {
				t.Run(m.name, func(t *testing.T) {
					dir := copySet(t, srcDir)
					dataPath := filepath.Join(dir, "data.bin")
					if m.apply != nil {
						m.apply(t, dataPath, tc.size)
					}
					compareWithGopar(t, dataPath+".par2")
				})
			}
		})
	}
}

// Volume-level cases: a removed volume leaves the data intact (parity=0 is
// still OK), a corrupted volume makes the set unverifiable.
func TestVerifyStreamOracle_Volumes(t *testing.T) {
	if testing.Short() {
		t.Skip("oracle corpus is generated with gopar")
	}

	t.Run("volume removed", func(t *testing.T) {
		dir := t.TempDir()
		data := writeCorpus(t, dir, 1_000_000, 4096)
		matches, err := filepath.Glob(data + ".vol*.par2")
		require.NoError(t, err)
		require.NotEmpty(t, matches)
		for _, m := range matches {
			require.NoError(t, os.Remove(m))
		}
		compareWithGopar(t, data+".par2")
	})

	t.Run("volume corrupted", func(t *testing.T) {
		dir := t.TempDir()
		data := writeCorpus(t, dir, 1_000_000, 4096)
		matches, err := filepath.Glob(data + ".vol*.par2")
		require.NoError(t, err)
		require.NotEmpty(t, matches)

		raw, err := os.ReadFile(matches[0])
		require.NoError(t, err)
		raw[len(raw)/2] ^= 0xFF
		require.NoError(t, os.WriteFile(matches[0], raw, 0o644))

		compareWithGopar(t, data+".par2")
	})
}

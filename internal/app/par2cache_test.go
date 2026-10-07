package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Item 7.2: the session cache of parsed .par2 contents — misses before the
// first parse, hits for unchanged files, invalidation on change, forget.
func TestPar2NameCache(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "a.txt", []byte("content"))
	par2Path := filepath.Join(dir, "a.txt.par2")

	c := newPar2NameCache()

	if _, ok := c.get(dir, "a.txt.par2"); ok {
		t.Fatal("the cache must miss before the first parse")
	}

	e := c.put(dir, "a.txt.par2")
	assert.Empty(t, e.err)
	assert.Equal(t, []string{"a.txt"}, e.names)

	got, ok := c.get(dir, "a.txt.par2")
	require.True(t, ok, "an unchanged file must be a hit")
	assert.Equal(t, []string{"a.txt"}, got.names)

	// any change of the file invalidates the entry
	future := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(par2Path, future, future))
	if _, ok := c.get(dir, "a.txt.par2"); ok {
		t.Fatal("a changed file must be a miss")
	}

	// the fresh parse re-populates the cache
	c.put(dir, "a.txt.par2")
}

// Broken archives are cached with the error, so the honest status survives
// rebuilds without re-reading the file
func TestPar2NameCache_BrokenFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.par2"), []byte("not a par2 file"), 0o644))

	c := newPar2NameCache()
	e := c.put(dir, "broken.par2")
	assert.NotEmpty(t, e.err)
	assert.Empty(t, e.names)

	got, ok := c.get(dir, "broken.par2")
	require.True(t, ok)
	assert.NotEmpty(t, got.err)
}

// seed records a known parse result without parsing
func TestPar2NameCache_Seed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt.par2"), []byte("pretend archive"), 0o644))

	c := newPar2NameCache()
	c.seed(dir, "a.txt.par2", []string{"a.txt"})

	got, ok := c.get(dir, "a.txt.par2")
	require.True(t, ok)
	assert.Equal(t, []string{"a.txt"}, got.names)
}

// Item 7.2: a cache hit of a broken archive keeps the Unverifiable status
// across rebuilds without any par2 IO
func TestProcessIndexFiles_CachedBrokenStaysUnverifiable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.par2"), []byte("not a par2 file"), 0o644))

	m := newTestModel()
	m.currentDir = dir

	// first scan: not parsed yet, pending for the background load
	osFiles, err := os.ReadDir(dir)
	require.NoError(t, err)
	scan := m.processIndexFiles(osFiles)
	require.Len(t, scan.items, 1)
	assert.Len(t, scan.pending, 1)

	// the parse discovers the broken file and caches the error
	cmd := m.loadArchivedCmd(dir, scan.pending)
	_, _ = m.Update(cmd())

	// rebuild: the cached error is applied synchronously, nothing pending
	osFiles, err = os.ReadDir(dir)
	require.NoError(t, err)
	scan = m.processIndexFiles(osFiles)
	require.Len(t, scan.items, 1)
	assert.Empty(t, scan.pending)
	it := scan.items[0].(indexListItem)
	assert.Equal(t, index.StatusUnverifiable, it.status)
	assert.Contains(t, it.desc, "не удалось прочитать")
}

// A UTF-8 data file must be marked archived by the exact byte match of the
// parsed name (plans/utf8_filenames.md §5) — the parse must not transcode
// the name, or the mark never lands.
func TestBuildModelForDirectory_UTF8NamesMarkedArchived(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "отчёт №5.bin", []byte("content"))

	m := newTestModel()
	m.currentDir = dir
	cmd, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	_, _ = m.Update(cmd())

	assert.Equal(t, []string{"отчёт №5.bin"}, m.archived)
}

// Item 7.2: a fully cached directory produces no background load at all
func TestBuildModelForDirectory_FullyCachedNoCommand(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "a.txt", []byte("content"))

	m := newTestModel()
	cmd, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	_, _ = m.Update(cmd())

	// second build: everything is parsed and fresh
	cmd, err = m.buildModelForDirectory(dir)
	require.NoError(t, err)
	assert.Nil(t, cmd, "a fully cached directory needs no background load")
	assert.Equal(t, []string{"a.txt"}, m.archived, "marks are restored from the cache without par2 IO")
}

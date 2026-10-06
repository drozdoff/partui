package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"partui/internal/index"
	"partui/internal/par2"

	gopar "github.com/akalin/gopar/par2"
	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubItem struct{}

func (stubItem) Title() string       { return "stub" }
func (stubItem) Description() string { return "" }
func (stubItem) FilterValue() string { return "stub" }

func prepareDirectory(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()

	fileNames := []string{"a.txt", "b.bin"}
	for _, name := range fileNames {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("content"), 0o644); err != nil {
			t.Fatalf("failed to create temp file %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(tempDir, "cdir"), 0o755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	return tempDir
}

func TestBuildModelForDirectory(t *testing.T) {
	dir := prepareDirectory(t)
	m := NewModel(newTestApp())
	_, err := m.buildModelForDirectory(dir)

	assert.Nil(t, err)

	assert.Equal(t, dir, m.currentDir)

	// Expect 4 entries
	assert.Equal(t, 4, len(m.fileList.Items()))
	expected := []struct {
		title string
		size  uint64
		isDir bool
	}{
		{"../", 0, true},
		{"cdir/", 0, true},
		{"a.txt", 7, false},
		{"b.bin", 7, false},
	}

	for i, f := range m.fileList.Items() {
		item := f.(fileListItem)
		assert.Equal(t, expected[i].title, item.title)
		if !item.isDir {
			assert.Equal(t, expected[i].size, item.size)
		}
	}

	_, err = m.buildModelForDirectory(filepath.Join(os.TempDir(), "non-existent-dir"))
	assert.Error(t, err)
}

// Regression test for item 1.2: findDirIndex must return -1 when the entry
// is missing and a valid index (including 0) when it is found.
func TestFindDirIndex(t *testing.T) {
	items := []list.Item{
		fileListItem{title: "../", isDir: true},
		fileListItem{title: "sub/", isDir: true},
		fileListItem{title: "file.txt"},
	}

	assert.Equal(t, 1, findDirIndex(items, "/tmp/sub"))
	// the first element must be findable, not reported as "not found"
	assert.Equal(t, 0, findDirIndex(items, "/tmp/.."))

	assert.Equal(t, -1, findDirIndex(items, "/tmp/missing"))
	assert.Equal(t, -1, findDirIndex([]list.Item{stubItem{}}, "/tmp/stub"))
}

func TestIsParIndex(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     bool
	}{
		{"plain par2 index", "archive.par2", true},
		{"uppercase par2 index", "ARCHIVE.PAR2", true},
		{"volume par2 should be false", "archive.vol000+01.par2", false},
		{"volume par2 mixed case should be false", "archive.VOL123+45.PAR2", false},
		{"not par2 extension", "archive.par", false},
		{"missing plus separator", "archive.vol000-01.par2", true}, // not a valid volume pattern, thus index
		{"non-digit volume parts", "archive.volabc+de.par2", true}, // not matched by the volume pattern
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isParIndex(tc.filename)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Items 3.2 and 7.2: a .par2 file that cannot be parsed must not silently
// disappear from the index list — it is shown with the Unverifiable status.
// The parse itself moved to the background load: until it reports, the
// status is the optimistic default.
func TestBuildModelForDirectory_UnreadablePar2(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.par2"), []byte("not a par2 file at all"), 0o644))

	m := NewModel(newTestApp())
	cmd, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	require.NotNil(t, cmd, "an unparsed archive must be loaded in the background")

	it := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, "broken.par2", it.par2Filename)
	assert.Equal(t, index.StatusOK, it.status, "the status is not known before the load")

	// run the load and feed the result through Update
	_, _ = m.Update(cmd())

	items := m.indexList.Items()
	require.Len(t, items, 1)
	it = items[0].(indexListItem)
	assert.Equal(t, index.StatusUnverifiable, it.status)
	assert.Contains(t, it.desc, "не удалось прочитать")

	// the broken archive must not have entered the database
	desc, err := m.app.store.Get(dir, "broken.par2")
	require.NoError(t, err)
	assert.Nil(t, desc)

	// the file itself stays hidden from the file list as before
	// (the only entry is the "../" parent directory)
	require.Len(t, m.fileList.Items(), 1)
	assert.Equal(t, "../", m.fileList.Items()[0].(fileListItem).title)
}

// Item 6.1: show_hidden in the config makes dotfiles visible; .par2 files
// stay in the index list either way.
func TestBuildModelForDirectory_ShowHidden(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"visible.txt", ".hidden.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644))
	}

	titles := func(m *Model) []string {
		items := m.fileList.Items()
		result := make([]string, 0, len(items))
		for _, it := range items {
			result = append(result, it.(fileListItem).title)
		}
		return result
	}

	m := newTestModel()
	_, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	assert.Contains(t, titles(m), "visible.txt")
	assert.NotContains(t, titles(m), ".hidden.txt")

	m.app.cfg.ShowHidden = true
	_, err = m.buildModelForDirectory(dir)
	require.NoError(t, err)
	assert.Contains(t, titles(m), ".hidden.txt")
}

func TestShortReason(t *testing.T) {
	assert.Equal(t, "boom", shortReason("boom"))
	assert.Equal(t, "first line", shortReason("first line\nsecond line"))

	long := strings.Repeat("а", 100)
	got := shortReason(long)
	assert.Len(t, []rune(got), 61) // 60 runes + the ellipsis
	assert.True(t, strings.HasSuffix(got, "…"))
}

// writeValidPar2 creates a real .par2 archive for a small data file, with
// the same options createParFiles uses
func writeValidPar2(t *testing.T, dir, title string, content []byte) {
	t.Helper()
	data := filepath.Join(dir, title)
	require.NoError(t, os.WriteFile(data, content, 0o644))
	require.NoError(t, goparRunner{}.Create(data+".par2", []string{data}, gopar.CreateOptions{
		SliceByteCount:  par2.CalculateBlockSize(uint64(len(content)), 5),
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))
}

// readIndexItems reads dir and builds the index list items from it
func readIndexItems(t *testing.T, m *Model) []indexListItem {
	t.Helper()
	osFiles, err := os.ReadDir(m.currentDir)
	require.NoError(t, err)
	scan := m.processIndexFiles(osFiles)
	result := make([]indexListItem, len(scan.items))
	for i, it := range scan.items {
		result[i] = it.(indexListItem)
	}
	return result
}

// Item 5.1: an index already present in the database keeps its stored
// status and LastVerified.
func TestProcessIndexFiles_UsesStoredDescriptor(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "a.txt", []byte("content"))

	old := time.Unix(1700000000, 0)
	st := newFakeStore()
	require.NoError(t, st.Put(dir, "a.txt.par2", index.StatusDamaged, old))

	m := newTestModel()
	m.app.store = st
	m.currentDir = dir

	items := readIndexItems(t, m)
	require.Len(t, items, 1)
	assert.Equal(t, index.StatusDamaged, items[0].status)
	assert.WithinDuration(t, old, items[0].lastVerified, time.Second)
}

// Item 7.2: a new .par2 file is stored as OK by the background load, not
// during the model build — unparseable files must never enter the database
func TestLoadArchived_StoresNewIndexAsOK(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "a.txt", []byte("content"))

	fixed := time.Unix(1700000000, 0)
	st := newFakeStore()
	m := newTestModel()
	m.app.clock = func() time.Time { return fixed }
	m.app.store = st

	cmd, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	desc, err := st.Get(dir, "a.txt.par2")
	require.NoError(t, err)
	assert.Nil(t, desc, "nothing is stored before the archive is parsed")

	_, _ = m.Update(cmd())

	desc, err = st.Get(dir, "a.txt.par2")
	require.NoError(t, err)
	require.NotNil(t, desc, "the record must be persisted under the .par2 name")
	assert.Equal(t, index.StatusOK, desc.Status)
	assert.Equal(t, fixed, desc.LastVerified, "a fresh record gets the current time")

	it := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, fixed, it.lastVerified, "the item shows the record time")
}

// Item 7.2: the background load marks the source files as archived; a
// result of a directory the user has already left is dropped
func TestLoadArchived_MarksFilesAndDropsForeignDir(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "a.txt", []byte("content"))

	m := newTestModel()
	cmd, err := m.buildModelForDirectory(dir)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	it := m.fileList.Items()[1].(fileListItem) // "../", "a.txt"
	require.Equal(t, "a.txt", it.title)
	assert.False(t, it.archive)

	// a foreign directory must not touch the model
	_, _ = m.Update(archivedLoadedMsg{dir: "/somewhere/else", results: []par2Content{
		{par2Filename: "a.txt.par2", names: []string{"a.txt"}},
	}})
	it = m.fileList.Items()[1].(fileListItem)
	assert.False(t, it.archive, "a foreign result must be dropped")

	_, _ = m.Update(cmd())

	it = m.fileList.Items()[1].(fileListItem)
	assert.True(t, it.archive)
	assert.Contains(t, m.archived, "a.txt")
}

// Item 7.2: archive marks of one directory must not leak into another one
func TestBuildModelForDirectory_ResetsArchivedMarks(t *testing.T) {
	dirA := t.TempDir()
	writeValidPar2(t, dirA, "a.txt", []byte("content"))

	dirB := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "b.txt"), []byte("content"), 0o644))

	m := newTestModel()
	cmd, err := m.buildModelForDirectory(dirA)
	require.NoError(t, err)
	_, _ = m.Update(cmd())
	require.True(t, m.fileList.Items()[1].(fileListItem).archive)

	// navigating away resets the marks; the stale load of dirA was already
	// consumed above, so nothing re-marks the files of dirB
	_, err = m.buildModelForDirectory(dirB)
	require.NoError(t, err)

	it := m.fileList.Items()[1].(fileListItem) // "../", "b.txt"
	assert.Equal(t, "b.txt", it.title)
	assert.False(t, it.archive)
	assert.Empty(t, m.archived)
}

// A failing store does not hide the index: the item stays visible with the
// default status, the failure is only logged
func TestProcessIndexFiles_StoreErrorKeepsItem(t *testing.T) {
	dir := t.TempDir()
	writeValidPar2(t, dir, "a.txt", []byte("content"))

	st := newFakeStore()
	st.err = errors.New("db is broken")
	m := newTestModel()
	m.app.store = st
	m.currentDir = dir

	items := readIndexItems(t, m)
	require.Len(t, items, 1)
	assert.Equal(t, index.StatusOK, items[0].status)
	assert.True(t, items[0].lastVerified.IsZero())
}

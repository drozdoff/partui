package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubItem struct{}

func (stubItem) Title() string       { return "stub" }
func (stubItem) Description() string { return "" }
func (stubItem) FilterValue() string { return "stub" }

func prepareDirecrory(t *testing.T) string {
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
	dir := prepareDirecrory(t)
	m := NewModel(newTestApp())
	err := m.buildModelForDirectory(dir)

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

	err = m.buildModelForDirectory(filepath.Join(os.TempDir(), "non-existent-dir"))
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

// Item 3.2: a .par2 file that cannot be parsed must not silently disappear
// from the index list — it is shown with the Unverifiable status.
func TestBuildModelForDirectory_UnreadablePar2(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.par2"), []byte("not a par2 file at all"), 0o644))

	m := NewModel(newTestApp())
	require.NoError(t, m.buildModelForDirectory(dir))

	items := m.indexList.Items()
	require.Len(t, items, 1)
	it := items[0].(indexListItem)
	assert.Equal(t, "broken.par2", it.par2Filename)
	assert.Equal(t, index.StatusUnverifiable, it.status)
	assert.Contains(t, it.desc, "не удалось прочитать")

	// the file itself stays hidden from the file list as before
	// (the only entry is the "../" parent directory)
	require.Len(t, m.fileList.Items(), 1)
	assert.Equal(t, "../", m.fileList.Items()[0].(fileListItem).title)
}

func TestShortReason(t *testing.T) {
	assert.Equal(t, "boom", shortReason(errors.New("boom")))
	assert.Equal(t, "first line", shortReason(errors.New("first line\nsecond line")))

	long := strings.Repeat("а", 100)
	got := shortReason(errors.New(long))
	assert.Len(t, []rune(got), 61) // 60 runes + the ellipsis
	assert.True(t, strings.HasSuffix(got, "…"))
}

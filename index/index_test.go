package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func TestOpenDB_CreatesNewDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "new.db")

	// Database file should not exist yet
	_, err := os.Stat(dbPath)
	assert.True(t, os.IsNotExist(err))

	// OpenDB should create the database
	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	// Database file should now exist
	info, err := os.Stat(dbPath)
	assert.NoError(t, err)
	assert.False(t, info.IsDir())
}

func TestOpenDB_OpensExistingDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "existing.db")

	// Create initial database
	db1, err := OpenDB(dbPath)
	require.NoError(t, err)
	CloseDB(db1)

	// Open the same database again
	db2, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db2)
}

func TestOpenDB_CreatesBuckets(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "buckets.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	// Verify buckets exist
	err = db.View(func(tx *bbolt.Tx) error {
		indexes := tx.Bucket([]byte(BucketIndexes))
		assert.NotNil(t, indexes, "indexes bucket should exist")

		byPath := tx.Bucket([]byte(BucketByPath))
		assert.NotNil(t, byPath, "by_path bucket should exist")
		return nil
	})
	assert.NoError(t, err)
}

func TestGetIndex(t *testing.T) {
	// Create a temporary database
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	defer CloseDB(db)

	// Test case 1: Non-existent path should return false
	descriptor, err := GetIndex(db, "/some/dir", "nonexistent.txt")
	assert.NoError(t, err)
	assert.Nil(t, descriptor)

	// Add an index entry
	dir := "/test/dir"
	filename := "testfile.txt"
	lastVerified := time.Now().Truncate(time.Second)
	err = PutIndex(db, dir, filename, StatusOK, lastVerified, 0)
	require.NoError(t, err)

	// Test case 2: Existing path should return descriptor
	descriptor, err = GetIndex(db, dir, filename)
	assert.NoError(t, err)
	assert.NotNil(t, descriptor)
	assert.Equal(t, StatusOK, descriptor.Status)
	assert.Equal(t, lastVerified, descriptor.LastVerified)

	// Test case 3: Wrong directory should return false
	descriptor, err = GetIndex(db, "/wrong/dir", filename)
	assert.NoError(t, err)
	assert.Nil(t, descriptor)

	// Test case 4: Wrong filename should return false
	descriptor, err = GetIndex(db, dir, "wrongfile.txt")
	assert.NoError(t, err)
	assert.Nil(t, descriptor)

	// Test case 5: Update existing entry and check that Exists returns new key
	newLastVerified := time.Now().Add(time.Hour)
	err = PutIndex(db, dir, filename, StatusDamaged, newLastVerified, 0)
	require.NoError(t, err)

	descriptor, err = GetIndex(db, dir, filename)
	assert.NoError(t, err)
	assert.NotNil(t, descriptor)

	// Verify the key contains the new timestamp
	assert.Equal(t, StatusDamaged, descriptor.Status)
}

func TestGetIndex_EmptyDB(t *testing.T) {
	// Create a temporary database with no data
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "empty.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	// Any query on empty DB should return false
	descriptor, err := GetIndex(db, "/any", "file.txt")
	assert.NoError(t, err)
	assert.Nil(t, descriptor)
}

func TestGetIndex_MultipleFiles(t *testing.T) {
	// Create a temporary database
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	// Add multiple files in the same directory
	dir := "/multi/dir"
	files := []string{"file1.txt", "file2.txt", "file3.txt"}

	for _, f := range files {
		err = PutIndex(db, dir, f, StatusOK, time.Now(), 0)
		require.NoError(t, err)
	}

	// Verify all files exist
	for _, f := range files {
		descriptor, err := GetIndex(db, dir, f)
		assert.NoError(t, err)
		assert.NotNil(t, descriptor, "file %s should exist", f)
	}

	// Verify non-existent file doesn't exist
	descriptor, err := GetIndex(db, dir, "nonexistent.txt")
	assert.NoError(t, err)
	assert.Nil(t, descriptor)
}

func TestGetIndex_DifferentDirectories(t *testing.T) {
	// Create a temporary database
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	// Add files in different directories
	files := []struct {
		dir      string
		filename string
	}{
		{"/dir1", "file.txt"},
		{"/dir2", "file.txt"},
		{"/dir1", "other.txt"},
	}

	for _, f := range files {
		err = PutIndex(db, f.dir, f.filename, StatusOK, time.Now(), 0)
		require.NoError(t, err)
	}

	// Verify each file exists
	testCases := []struct {
		dir      string
		filename string
		exists   bool
	}{
		{"/dir1", "file.txt", true},
		{"/dir2", "file.txt", true},
		{"/dir1", "other.txt", true},
		{"/dir3", "file.txt", false},
		{"/dir1", "missing.txt", false},
	}

	for _, tc := range testCases {
		descriptor, err := GetIndex(db, tc.dir, tc.filename)
		assert.NoError(t, err)
		if tc.exists {
			assert.NotNil(t, descriptor, "dir=%s, file=%s", tc.dir, tc.filename)
		} else {
			assert.Nil(t, descriptor, "dir=%s, file=%s", tc.dir, tc.filename)
		}
	}
}

func TestPutIndex_Basic(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	dir := "/test/dir"
	filename := "testfile.txt"
	lastVerified := time.Now().Truncate(time.Second)

	err = PutIndex(db, dir, filename, StatusOK, lastVerified, 0)

	assert.NoError(t, err)

	// Verify the entry was saved correctly
	descriptor, err := GetIndex(db, dir, filename)
	require.NoError(t, err)
	require.NotNil(t, descriptor)
	assert.Equal(t, filename, descriptor.Filename)
	assert.Equal(t, dir, descriptor.Dir)
	assert.Equal(t, StatusOK, descriptor.Status)
	assert.Equal(t, lastVerified, descriptor.LastVerified)
}

func TestPutIndex_UpdateExisting(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	dir := "/test/dir"
	filename := "testfile.txt"
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	newTime := time.Date(2024, 6, 15, 12, 30, 0, 0, time.Local)

	// Add initial entry
	err = PutIndex(db, dir, filename, StatusOK, oldTime, 0)
	require.NoError(t, err)

	// Update with new time and status
	err = PutIndex(db, dir, filename, StatusDamaged, newTime, 0)
	require.NoError(t, err)

	// Verify only the new entry exists
	descriptor, err := GetIndex(db, dir, filename)
	require.NoError(t, err)
	require.NotNil(t, descriptor)
	assert.Equal(t, StatusDamaged, descriptor.Status)
	assert.Equal(t, newTime.Unix(), descriptor.LastVerified.Unix())

	// Verify only one entry exists in the database
	all, err := All(db)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestPutIndex_DifferentStatuses(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	dir := "/test/dir"
	filename := "testfile.txt"
	lastVerified := time.Now()

	testCases := []struct {
		status   Status
		expected Status
	}{
		{StatusOK, StatusOK},
		{StatusProcessing, StatusProcessing},
		{StatusDamaged, StatusDamaged},
	}

	for _, tc := range testCases {
		err = PutIndex(db, dir, filename, tc.status, lastVerified, 0)
		require.NoError(t, err)

		descriptor, err := GetIndex(db, dir, filename)
		require.NoError(t, err)
		require.NotNil(t, descriptor)
		assert.Equal(t, tc.expected, descriptor.Status, "Status should be %s", StatusDisplay(tc.status))
	}
}

func TestPutIndex_MultipleFilesSameDir(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	dir := "/test/dir"
	files := []string{"file1.txt", "file2.txt", "file3.txt"}

	for i, f := range files {
		lastVerified := time.Now().Add(time.Duration(i) * time.Hour)
		err = PutIndex(db, dir, f, StatusOK, lastVerified, 0)
		require.NoError(t, err)
	}

	// Verify all files exist
	all, err := All(db)
	require.NoError(t, err)
	assert.Len(t, all, len(files))

	// Verify each file can be retrieved
	for _, f := range files {
		descriptor, err := GetIndex(db, dir, f)
		require.NoError(t, err)
		require.NotNil(t, descriptor, "file %s should exist", f)
	}
}

func TestPutIndex_EmptyDirAndFilename(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	dir := ""
	filename := "testfile.txt"
	lastVerified := time.Now()

	err = PutIndex(db, dir, filename, StatusOK, lastVerified, 0)
	require.NoError(t, err)

	descriptor, err := GetIndex(db, dir, filename)
	require.NoError(t, err)
	require.NotNil(t, descriptor)
	assert.Equal(t, filename, descriptor.Filename)
	assert.Equal(t, dir, descriptor.Dir)
}

func TestPutIndex_DifferentDirectories(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	files := []struct {
		dir      string
		filename string
	}{
		{"/dir1", "file.txt"},
		{"/dir2", "file.txt"},
		{"/dir1", "other.txt"},
		{"", "empty.txt"},
	}

	for _, f := range files {
		err = PutIndex(db, f.dir, f.filename, StatusOK, time.Now(), 0)
		require.NoError(t, err)
	}

	// Verify all unique paths exist
	all, err := All(db)
	require.NoError(t, err)
	assert.Len(t, all, len(files))
}

func TestGetOldIndexes_Basic(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	now := time.Now()
	oldTime := now.Add(-60 * 24 * time.Hour) // 60 days ago

	dir := "/test/dir"

	// Add old index (should be returned)
	err = PutIndex(db, dir, "old.txt", StatusOK, oldTime, 0)
	require.NoError(t, err)

	// Add recent index (should NOT be returned)
	err = PutIndex(db, dir, "recent.txt", StatusOK, now, 0)
	require.NoError(t, err)

	// Query for indexes older than 30 days
	threshold := now.Add(-30 * 24 * time.Hour)
	oldIndexes, err := GetOldIndexes(db, threshold)
	require.NoError(t, err)

	assert.Len(t, oldIndexes, 1)
	assert.Equal(t, "old.txt", oldIndexes[0].Filename)
}

func TestGetOldIndexes_EmptyDB(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "empty.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	threshold := time.Now()
	oldIndexes, err := GetOldIndexes(db, threshold)
	require.NoError(t, err)

	assert.Empty(t, oldIndexes)
}

func TestGetOldIndexes_AllIndexesOld(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	dir := "/test/dir"
	files := []string{"file1.txt", "file2.txt", "file3.txt"}

	for _, f := range files {
		err = PutIndex(db, dir, f, StatusOK, oldTime, 0)
		require.NoError(t, err)
	}

	threshold := time.Now().Add(-30 * 24 * time.Hour)
	oldIndexes, err := GetOldIndexes(db, threshold)
	require.NoError(t, err)

	assert.Len(t, oldIndexes, 3)
}

func TestGetOldIndexes_ThresholdExcludesRecent(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	newTime := time.Now()
	dir := "/test/dir"

	// Add old index
	err = PutIndex(db, dir, "old.txt", StatusOK, oldTime, 0)
	require.NoError(t, err)

	// Add very recent index (1 hour ago - should NOT be old)
	recentTime := newTime.Add(-1 * time.Hour)
	err = PutIndex(db, dir, "recent.txt", StatusOK, recentTime, 0)
	require.NoError(t, err)

	// Threshold is 30 days ago - only old.txt should be returned
	threshold := newTime.Add(-30 * 24 * time.Hour)
	oldIndexes, err := GetOldIndexes(db, threshold)
	require.NoError(t, err)

	// Should only return old.txt (recent.txt is only 1 hour old)
	assert.Len(t, oldIndexes, 1)
	assert.Equal(t, "old.txt", oldIndexes[0].Filename)
}

func TestGetOldIndexes_DifferentDirectories(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := OpenDB(dbPath)
	require.NoError(t, err)
	defer CloseDB(db)

	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	recentTime := time.Now()

	// Add old indexes in different directories
	err = PutIndex(db, "/dir1", "file1.txt", StatusOK, oldTime, 0)
	require.NoError(t, err)
	err = PutIndex(db, "/dir2", "file2.txt", StatusOK, oldTime, 0)
	require.NoError(t, err)

	// Add recent index
	err = PutIndex(db, "/dir3", "file3.txt", StatusOK, recentTime, 0)
	require.NoError(t, err)

	threshold := time.Now().Add(-30 * 24 * time.Hour)
	oldIndexes, err := GetOldIndexes(db, threshold)
	require.NoError(t, err)

	assert.Len(t, oldIndexes, 2)

	// Verify both directories are represented
	dirs := make(map[string]bool)
	for _, idx := range oldIndexes {
		dirs[idx.Dir] = true
	}
	assert.True(t, dirs["/dir1"])
	assert.True(t, dirs["/dir2"])
	assert.False(t, dirs["/dir3"])
}

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
	err = PutIndex(db, dir, filename, StatusOK, lastVerified)
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
	err = PutIndex(db, dir, filename, StatusDamaged, newLastVerified)
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
		err = PutIndex(db, dir, f, StatusOK, time.Now())
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
		err = PutIndex(db, f.dir, f.filename, StatusOK, time.Now())
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

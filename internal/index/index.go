package index

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// Status represents the status of a PAR2 index file
type Status byte

const (
	StatusOK           Status = 'o'
	StatusProcessing   Status = 'p'
	StatusDamaged      Status = 'd'
	StatusUnverifiable Status = 'u'
	StatusMissing      Status = 'm'
)

// StatusDisplay returns the display string for a status
func StatusDisplay(s Status) string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusProcessing:
		return "Processing"
	case StatusDamaged:
		return "Damaged"
	case StatusUnverifiable:
		return "Unverifiable"
	case StatusMissing:
		return "Missing"
	default:
		return "Unknown"
	}
}

// Bucket names
const (
	BucketIndexes = "indexes" // primary: key = timestamp+status+dir+filename
	BucketByPath  = "by_path" // lookup: key = dir+filename -> primary key
	BucketDamaged = "damaged" // set of damaged indexes: key = dir+filename
)

// bucketNames returns bucket names as byte slices
func bucketName(name string) []byte {
	return []byte(name)
}

// IndexDescriptor represents an index file in the database
type IndexDescriptor struct {
	Filename     string
	Dir          string
	Status       Status
	LastVerified time.Time
}

// GetDBPath returns the default path to the index database
func GetDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".partui", "indexes.db")
}

// EnsureDir creates the directory for the given dbPath if it doesn't exist
func EnsureDir(dbPath string) error {
	dir := filepath.Dir(dbPath)
	return os.MkdirAll(dir, 0700)
}

// OpenDB opens/creates a BoltDB database with two buckets.
// If the database file doesn't exist, it will be created on first call.
// The dbPath parameter allows for testing with custom paths.
func OpenDB(dbPath string) (*bbolt.DB, error) {
	// Extract directory from path and ensure it exists
	if err := EnsureDir(dbPath); err != nil {
		return nil, err
	}

	db, err := bbolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketName(BucketIndexes))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(bucketName(BucketByPath))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(bucketName(BucketDamaged))
		return err
	})

	return db, err
}

func CloseDB(db *bbolt.DB) {
	if db != nil {
		db.Close()
	}
}

// GenerateKey creates a primary key: bigendian timestamp + status + dir + filename
func GenerateKey(dir, filename string, lastVerified time.Time, status Status) []byte {
	ts := uint64(lastVerified.Unix())
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, ts)

	key := append(tsBytes, byte(status))
	return append(key, []byte(dir+"/"+filename)...)
}

// GenerateLookupKey creates a lookup key: dir + filename
func GenerateLookupKey(dir, filename string) []byte {
	return []byte(dir + "/" + filename)
}

// PutIndex saves an IndexDescriptor to the database (value is empty - all data is in the key)
func PutIndex(db *bbolt.DB, dir, filename string, status Status, lastVerified time.Time) error {
	key := GenerateKey(dir, filename, lastVerified, status)
	lookupKey := GenerateLookupKey(dir, filename)

	return db.Update(func(tx *bbolt.Tx) error {
		indexes := tx.Bucket(bucketName(BucketIndexes))
		byPath := tx.Bucket(bucketName(BucketByPath))
		damaged := tx.Bucket(bucketName(BucketDamaged))

		// If record already exists, delete the old one
		oldKey := byPath.Get(lookupKey)
		if oldKey != nil {
			indexes.Delete(oldKey)
		}

		// Save to primary bucket (value is empty)
		indexes.Put(key, nil)

		// Update lookup bucket
		byPath.Put(lookupKey, key)

		// Manage damaged bucket based on status
		if status == StatusDamaged {
			// Add to damaged bucket
			damaged.Put(lookupKey, nil)
		} else {
			// Remove from damaged bucket if present (status is OK or Processing)
			damaged.Delete(lookupKey)
		}

		return nil
	})
}

// GetIndex retrieves an IndexDescriptor by dir+filename
func GetIndex(db *bbolt.DB, dir, filename string) (*IndexDescriptor, error) {
	lookupKey := GenerateLookupKey(dir, filename)
	var primaryKey []byte

	err := db.View(func(tx *bbolt.Tx) error {
		byPath := tx.Bucket(bucketName(BucketByPath))
		primaryKey = byPath.Get(lookupKey)
		return nil
	})

	if err != nil {
		return nil, err
	}

	if primaryKey == nil {
		return nil, nil
	}

	descriptor := parsePrimaryKey(primaryKey)
	return &descriptor, nil
}

// UpdateIndexStatus updates only the status of an existing index, preserving LastVerified
func UpdateIndexStatus(db *bbolt.DB, dir, filename string, status Status) error {
	// Get the existing index to preserve LastVerified
	existing, err := GetIndex(db, dir, filename)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil // Index doesn't exist, nothing to update
	}

	// Generate new key with same LastVerified but new status
	newKey := GenerateKey(dir, filename, existing.LastVerified, status)
	lookupKey := GenerateLookupKey(dir, filename)

	return db.Update(func(tx *bbolt.Tx) error {
		indexes := tx.Bucket(bucketName(BucketIndexes))
		byPath := tx.Bucket(bucketName(BucketByPath))
		damaged := tx.Bucket(bucketName(BucketDamaged))

		// Get the old key
		oldKey := byPath.Get(lookupKey)
		if oldKey != nil {
			// Delete old entry
			indexes.Delete(oldKey)
		}

		// Save new entry with updated status but same timestamp
		indexes.Put(newKey, nil)
		byPath.Put(lookupKey, newKey)

		// Manage damaged bucket based on status
		if status == StatusDamaged {
			damaged.Put(lookupKey, nil)
		} else {
			damaged.Delete(lookupKey)
		}

		return nil
	})
}

// DeleteIndex removes the index record for dir+filename from all buckets.
// Deleting a missing record is a no-op.
func DeleteIndex(db *bbolt.DB, dir, filename string) error {
	lookupKey := GenerateLookupKey(dir, filename)

	return db.Update(func(tx *bbolt.Tx) error {
		indexes := tx.Bucket(bucketName(BucketIndexes))
		byPath := tx.Bucket(bucketName(BucketByPath))
		damaged := tx.Bucket(bucketName(BucketDamaged))

		oldKey := byPath.Get(lookupKey)
		if oldKey == nil {
			return nil
		}
		if err := indexes.Delete(oldKey); err != nil {
			return err
		}
		if err := byPath.Delete(lookupKey); err != nil {
			return err
		}
		return damaged.Delete(lookupKey)
	})
}

// All retrieves all IndexDescriptors
func All(db *bbolt.DB) ([]IndexDescriptor, error) {
	var result []IndexDescriptor

	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName(BucketIndexes))

		return b.ForEach(func(key, value []byte) error {
			// value is ignored - all data is in key
			result = append(result, parsePrimaryKey(key))
			return nil
		})
	})

	return result, err
}

// Key format: [8 bytes ts][1 byte status][dir/filename]
func parsePrimaryKey(key []byte) IndexDescriptor {
	ts := binary.BigEndian.Uint64(key[:8])
	status := Status(key[8])
	path := string(key[9:])

	// Split path to dir and filename (last slash)
	lastSlash := strings.LastIndex(path, "/")
	dir := path[:lastSlash]
	filename := path[lastSlash+1:]

	return IndexDescriptor{
		Filename:     filename,
		Dir:          dir,
		Status:       status,
		LastVerified: time.Unix(int64(ts), 0),
	}
}

// GetOldIndexes returns all index descriptors that were verified before the given threshold
// Uses efficient primary key scan since timestamp is the first part of the key
// Key format: [8 bytes big-endian timestamp][1 byte status][dir/filename]
func GetOldIndexes(db *bbolt.DB, threshold time.Time) ([]IndexDescriptor, error) {
	var result []IndexDescriptor

	thresholdTs := uint64(threshold.Unix())

	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketName(BucketIndexes))

		cursor := b.Cursor()

		// Iterate from the beginning up to threshold key
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			// Stop when we pass the threshold
			if len(k) >= 8 && binary.BigEndian.Uint64(k[:8]) >= thresholdTs {
				break
			}
			result = append(result, parsePrimaryKey(k))
		}
		return nil
	})

	return result, err
}

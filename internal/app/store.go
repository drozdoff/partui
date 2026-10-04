package app

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"partui/internal/index"

	"go.etcd.io/bbolt"
)

// ErrStoreUnavailable is returned by index store operations when the
// database could not be opened and the app runs in degraded mode
var ErrStoreUnavailable = errors.New("index store unavailable")

// IndexStore is the persistence interface for PAR2 indexes. It is defined
// here, at the consumer side, and implemented by bboltStore; noopStore
// stands in for the degraded mode when the database failed to open.
type IndexStore interface {
	Get(dir, filename string) (*index.IndexDescriptor, error)
	Put(dir, filename string, status index.Status, verifiedAt time.Time) error
	UpdateStatus(dir, filename string, status index.Status) error
	All() ([]index.IndexDescriptor, error)
	Old(threshold time.Time) ([]index.IndexDescriptor, error)
	Damaged() ([]index.IndexDescriptor, error)
	Count() (int, error)
	Close() error
}

// bboltStore implements IndexStore on top of a bbolt database opened via
// the index package. It is a thin wrapper: all logic lives in internal/index.
type bboltStore struct {
	db *bbolt.DB
}

// newBboltStore opens (or creates) the database at path
func newBboltStore(path string) (*bboltStore, error) {
	db, err := index.OpenDB(path)
	if err != nil {
		return nil, err
	}
	return &bboltStore{db: db}, nil
}

func (s *bboltStore) Get(dir, filename string) (*index.IndexDescriptor, error) {
	return index.GetIndex(s.db, dir, filename)
}

func (s *bboltStore) Put(dir, filename string, status index.Status, verifiedAt time.Time) error {
	return index.PutIndex(s.db, dir, filename, status, verifiedAt)
}

func (s *bboltStore) UpdateStatus(dir, filename string, status index.Status) error {
	return index.UpdateIndexStatus(s.db, dir, filename, status)
}

func (s *bboltStore) All() ([]index.IndexDescriptor, error) {
	return index.All(s.db)
}

func (s *bboltStore) Old(threshold time.Time) ([]index.IndexDescriptor, error) {
	return index.GetOldIndexes(s.db, threshold)
}

func (s *bboltStore) Damaged() ([]index.IndexDescriptor, error) {
	return index.GetDamaged(s.db)
}

func (s *bboltStore) Count() (int, error) {
	return index.CountAll(s.db)
}

func (s *bboltStore) Close() error {
	return s.db.Close()
}

// noopStore is the degraded-mode IndexStore: every operation fails with
// ErrStoreUnavailable. The degradation is logged once, not per call.
type noopStore struct {
	once   sync.Once
	logger *slog.Logger
}

func newNoopStore(logger *slog.Logger) *noopStore {
	return &noopStore{logger: logger}
}

// unavailable marks the degradation as logged and returns the sentinel error
func (s *noopStore) unavailable() error {
	s.once.Do(func() {
		s.logger.Warn("index store unavailable, running in degraded mode")
	})
	return ErrStoreUnavailable
}

func (s *noopStore) Get(dir, filename string) (*index.IndexDescriptor, error) {
	return nil, s.unavailable()
}

func (s *noopStore) Put(dir, filename string, status index.Status, verifiedAt time.Time) error {
	return s.unavailable()
}

func (s *noopStore) UpdateStatus(dir, filename string, status index.Status) error {
	return s.unavailable()
}

func (s *noopStore) All() ([]index.IndexDescriptor, error) {
	return nil, s.unavailable()
}

func (s *noopStore) Old(threshold time.Time) ([]index.IndexDescriptor, error) {
	return nil, s.unavailable()
}

func (s *noopStore) Damaged() ([]index.IndexDescriptor, error) {
	return nil, s.unavailable()
}

func (s *noopStore) Count() (int, error) {
	return 0, s.unavailable()
}

func (s *noopStore) Close() error {
	return s.unavailable()
}

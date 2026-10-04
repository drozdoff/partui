package app

import (
	"io"
	"log/slog"
	"sync"
	"time"

	"partui/internal/index"

	gopar "github.com/akalin/gopar/par2"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeStore is a map-backed IndexStore for tests; it never touches bbolt.
type fakeStore struct {
	mu    sync.Mutex
	items map[string]index.IndexDescriptor // key: dir + "/" + filename
	err   error                            // if set, every method fails
	puts  int
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: make(map[string]index.IndexDescriptor)}
}

func storeKey(dir, filename string) string { return dir + "/" + filename }

func (s *fakeStore) Get(dir, filename string) (*index.IndexDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if d, ok := s.items[storeKey(dir, filename)]; ok {
		return &d, nil
	}
	return nil, nil
}

func (s *fakeStore) Put(dir, filename string, status index.Status, verifiedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.puts++
	s.items[storeKey(dir, filename)] = index.IndexDescriptor{
		Filename:     filename,
		Dir:          dir,
		Status:       status,
		LastVerified: verifiedAt,
	}
	return nil
}

// UpdateStatus preserves LastVerified of an existing record and is a no-op
// for missing ones, mirroring bboltStore semantics
func (s *fakeStore) UpdateStatus(dir, filename string, status index.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	key := storeKey(dir, filename)
	d, ok := s.items[key]
	if !ok {
		return nil
	}
	d.Status = status
	s.items[key] = d
	return nil
}

func (s *fakeStore) All() ([]index.IndexDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	all := make([]index.IndexDescriptor, 0, len(s.items))
	for _, d := range s.items {
		all = append(all, d)
	}
	return all, nil
}

func (s *fakeStore) Old(threshold time.Time) ([]index.IndexDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	var old []index.IndexDescriptor
	for _, d := range s.items {
		if d.LastVerified.Before(threshold) {
			old = append(old, d)
		}
	}
	return old, nil
}

func (s *fakeStore) Delete(dir, filename string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	delete(s.items, storeKey(dir, filename))
	return nil
}

func (s *fakeStore) Close() error { return nil }

// fakePar2 records Par2Runner calls; results can be injected per test.
// The channels let tests wait for a call instead of polling.
type fakePar2 struct {
	mu         sync.Mutex
	createErr  error
	verifyRes  gopar.VerifyResult
	verifyErr  error
	created    []string // par paths passed to Create
	verified   []string // par paths passed to Verify
	createdCh  chan string
	verifiedCh chan string
}

func newFakePar2() *fakePar2 {
	return &fakePar2{
		createdCh:  make(chan string, 8),
		verifiedCh: make(chan string, 8),
	}
}

func (f *fakePar2) Create(parPath string, files []string, opts gopar.CreateOptions) error {
	f.mu.Lock()
	f.created = append(f.created, parPath)
	f.mu.Unlock()
	f.createdCh <- parPath
	return f.createErr
}

func (f *fakePar2) Verify(parPath string, opts gopar.VerifyOptions) (gopar.VerifyResult, error) {
	f.mu.Lock()
	f.verified = append(f.verified, parPath)
	f.mu.Unlock()
	f.verifiedCh <- parPath
	return f.verifyRes, f.verifyErr
}

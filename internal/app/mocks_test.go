package app

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"partui/internal/index"
	"partui/internal/par2"
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
// The channels let tests wait for a call instead of polling. The *Func
// fields, when set, replace the canned responses entirely — used to
// simulate a long operation that observes ctx cancellation.
type fakePar2 struct {
	mu         sync.Mutex
	createErr  error
	createFunc func(ctx context.Context, parPath string) error
	verifyRes  par2.VerifyResult
	verifyErr  error
	verifyFunc func(ctx context.Context, parPath string) (par2.VerifyResult, error)
	repairRes  par2.RepairResult
	repairErr  error
	repairFunc func(ctx context.Context, parPath string) (par2.RepairResult, error)
	created    []string // par paths passed to Create
	verified   []string // par paths passed to Verify
	repaired   []string // par paths passed to Repair
	createdCh  chan string
	verifiedCh chan string
	repairedCh chan string
}

func newFakePar2() *fakePar2 {
	return &fakePar2{
		createdCh:  make(chan string, 8),
		verifiedCh: make(chan string, 8),
		repairedCh: make(chan string, 8),
	}
}

func (f *fakePar2) Create(ctx context.Context, parPath string, files []string, opts par2.CreateOptions) error {
	f.mu.Lock()
	f.created = append(f.created, parPath)
	f.mu.Unlock()
	f.createdCh <- parPath
	if f.createFunc != nil {
		return f.createFunc(ctx, parPath)
	}
	return f.createErr
}

func (f *fakePar2) Verify(ctx context.Context, parPath string, opts par2.VerifyOptions) (par2.VerifyResult, error) {
	f.mu.Lock()
	f.verified = append(f.verified, parPath)
	f.mu.Unlock()
	f.verifiedCh <- parPath
	if f.verifyFunc != nil {
		return f.verifyFunc(ctx, parPath)
	}
	return f.verifyRes, f.verifyErr
}

func (f *fakePar2) Repair(ctx context.Context, parPath string, opts par2.RepairOptions) (par2.RepairResult, error) {
	f.mu.Lock()
	f.repaired = append(f.repaired, parPath)
	f.mu.Unlock()
	f.repairedCh <- parPath
	if f.repairFunc != nil {
		return f.repairFunc(ctx, parPath)
	}
	return f.repairRes, f.repairErr
}

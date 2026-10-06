package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBboltStore opens a throwaway bbolt store backed by a real
// temporary database file
func newTestBboltStore(t *testing.T) *bboltStore {
	t.Helper()
	st, err := newBboltStore(filepath.Join(t.TempDir(), "test.db"), context.Background(), discardLogger())
	require.NoError(t, err)
	return st
}

func TestNoopStore_AllMethodsUnavailable(t *testing.T) {
	st := newNoopStore(discardLogger())
	at := time.Unix(1700000000, 0)

	err := st.Put("/d", "a.par2", index.StatusOK, at)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	err = st.UpdateStatus("/d", "a.par2", index.StatusDamaged)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	desc, err := st.Get("/d", "a.par2")
	assert.Nil(t, desc)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	all, err := st.All()
	assert.Empty(t, all)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	old, err := st.Old(at)
	assert.Empty(t, old)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	err = st.Close()
	assert.ErrorIs(t, err, ErrStoreUnavailable)
}

func TestBboltStore_RoundTrip(t *testing.T) {
	st := newTestBboltStore(t)
	defer st.Close()

	verifiedAt := time.Unix(1700000000, 0)
	require.NoError(t, st.Put("/data", "a.par2", index.StatusOK, verifiedAt))

	desc, err := st.Get("/data", "a.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusOK, desc.Status)
	assert.WithinDuration(t, verifiedAt, desc.LastVerified, time.Second)

	// a missing record is no error and a nil descriptor
	desc, err = st.Get("/data", "missing.par2")
	require.NoError(t, err)
	assert.Nil(t, desc)

	// UpdateStatus preserves LastVerified
	require.NoError(t, st.UpdateStatus("/data", "a.par2", index.StatusDamaged))
	desc, err = st.Get("/data", "a.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusDamaged, desc.Status)
	assert.WithinDuration(t, verifiedAt, desc.LastVerified, time.Second)

	// UpdateStatus on a missing record is a no-op
	require.NoError(t, st.UpdateStatus("/data", "missing.par2", index.StatusDamaged))

	all, err := st.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, index.StatusDamaged, all[0].Status)

	old, err := st.Old(verifiedAt.Add(-time.Minute))
	require.NoError(t, err)
	assert.Empty(t, old)

	old, err = st.Old(verifiedAt.Add(time.Minute))
	require.NoError(t, err)
	assert.Len(t, old, 1)

	require.NoError(t, st.Close())

	// operations after Close must fail
	_, err = st.All()
	assert.Error(t, err)
}

func TestBboltStore_Delete(t *testing.T) {
	st := newTestBboltStore(t)
	defer st.Close()

	at := time.Unix(1700000000, 0)
	require.NoError(t, st.Put("/data", "a.par2", index.StatusOK, at))
	require.NoError(t, st.Put("/data", "b.par2", index.StatusDamaged, at))

	// deleting a missing record is a no-op
	require.NoError(t, st.Delete("/data", "missing.par2"))

	require.NoError(t, st.Delete("/data", "b.par2"))

	desc, err := st.Get("/data", "b.par2")
	require.NoError(t, err)
	assert.Nil(t, desc)

	all, err := st.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "a.par2", all[0].Filename)
}

func TestCleanupNonPar2Records(t *testing.T) {
	at := time.Unix(1700000000, 0)

	st := newFakeStore()
	require.NoError(t, st.Put("/data", "a.par2", index.StatusOK, at))
	require.NoError(t, st.Put("/data", "style.css", index.StatusUnverifiable, at))
	require.NoError(t, st.Put("/data", "readme", index.StatusUnverifiable, at))

	removed, err := cleanupNonPar2Records(st)
	require.NoError(t, err)
	require.Len(t, removed, 2)

	// only the .par2 record survives
	desc, err := st.Get("/data", "a.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)

	desc, err = st.Get("/data", "style.css")
	require.NoError(t, err)
	assert.Nil(t, desc)

	// idempotent: the second pass removes nothing
	removed, err = cleanupNonPar2Records(st)
	require.NoError(t, err)
	assert.Empty(t, removed)

	// a failing store reports the error
	st.err = errors.New("boom")
	_, err = cleanupNonPar2Records(st)
	assert.Error(t, err)
}

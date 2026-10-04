package app

import (
	"path/filepath"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	damaged, err := st.Damaged()
	assert.Empty(t, damaged)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	count, err := st.Count()
	assert.Zero(t, count)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	err = st.Close()
	assert.ErrorIs(t, err, ErrStoreUnavailable)
}

func TestBboltStore_RoundTrip(t *testing.T) {
	st, err := newBboltStore(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
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

	damaged, err := st.Damaged()
	require.NoError(t, err)
	require.Len(t, damaged, 1)
	assert.Equal(t, "a.par2", damaged[0].Filename)
	assert.Equal(t, "/data", damaged[0].Dir)

	all, err := st.All()
	require.NoError(t, err)
	require.Len(t, all, 1)

	old, err := st.Old(verifiedAt.Add(-time.Minute))
	require.NoError(t, err)
	assert.Empty(t, old)

	old, err = st.Old(verifiedAt.Add(time.Minute))
	require.NoError(t, err)
	assert.Len(t, old, 1)

	count, err := st.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	require.NoError(t, st.Close())

	// operations after Close must fail
	_, err = st.All()
	assert.Error(t, err)
}

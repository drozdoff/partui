package app

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"partui/internal/index"
)

// newTestApp returns an App with a discard logger and a nil db,
// suitable for tests that don't touch the database
func newTestApp() *App {
	app := NewApp(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// a buffered semaphore keeps launched goroutines from deadlocking
	// on machines with GOMAXPROCS=1
	app.workers = make(chan struct{}, 1)
	return app
}

func TestApp_StoreWrappers_NilDB(t *testing.T) {
	app := newTestApp()

	err := app.putIndexSafe("/d", "f.par2", index.StatusOK, time.Now())
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	err = app.updateIndexStatusSafe("/d", "f.par2", index.StatusProcessing)
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	_, err = app.allIndexesSafe()
	assert.ErrorIs(t, err, ErrStoreUnavailable)

	assert.NotPanics(t, func() { app.Shutdown() })
}

func TestApp_StoreWrappers_WithDB(t *testing.T) {
	db, err := index.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)

	app := newTestApp()
	app.db = db

	now := time.Now()
	assert.NoError(t, app.putIndexSafe("/d", "f.par2", index.StatusOK, now))

	desc, err := index.GetIndex(db, "/d", "f.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusOK, desc.Status)

	assert.NoError(t, app.updateIndexStatusSafe("/d", "f.par2", index.StatusDamaged))
	desc, err = index.GetIndex(db, "/d", "f.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusDamaged, desc.Status)

	all, err := app.allIndexesSafe()
	assert.NoError(t, err)
	assert.Len(t, all, 1)

	app.Shutdown()

	// any further operation must fail: the db is closed by Shutdown
	assert.Error(t, db.View(func(tx *bbolt.Tx) error { return nil }))
}

func TestApp_Shutdown_NilDB_NoPanic(t *testing.T) {
	app := newTestApp()
	assert.NotPanics(t, func() { app.Shutdown() })
}

func TestBottomPanelView_StoreUnavailable(t *testing.T) {
	bp := BottomPanel{storeErr: ErrStoreUnavailable}
	assert.Contains(t, bp.View(80), "DB: недоступна")

	assert.NotContains(t, BottomPanel{}.View(80), "DB: недоступна")
}

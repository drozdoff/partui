package app

import (
	"path/filepath"
	"testing"

	"partui/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestApp returns an App with a discard logger, a map-backed fake store
// and a fake PAR2 runner — no database and no filesystem access
func newTestApp() *App {
	app := NewApp(config.Defaults(), discardLogger())
	// a buffered semaphore keeps launched goroutines from deadlocking
	// on machines with GOMAXPROCS=1
	app.workers = make(chan struct{}, 1)
	app.store = newFakeStore()
	app.par2 = newFakePar2()
	return app
}

// newDegradedApp returns an App whose store is a noopStore, as after a
// failed database open
func newDegradedApp() *App {
	app := newTestApp()
	app.store = newNoopStore(app.logger)
	return app
}

func TestApp_Shutdown_NoopStore_NoPanic(t *testing.T) {
	app := newDegradedApp()
	assert.NotPanics(t, func() { app.Shutdown() })
}

func TestApp_Shutdown_ClosesStore(t *testing.T) {
	st, err := newBboltStore(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)

	app := newTestApp()
	app.store = st

	app.Shutdown()

	// any further operation must fail: the database is closed by Shutdown
	_, err = st.All()
	assert.Error(t, err)
}

func TestApp_DegradedMode_BottomPanel(t *testing.T) {
	app := newDegradedApp()

	bp := app.calculateBottomPanelStats()
	assert.ErrorIs(t, bp.storeErr, ErrStoreUnavailable)
	assert.Equal(t, 0, bp.totalCount)
	assert.Equal(t, 0, bp.processingCount)
}

func TestBottomPanelView_StoreUnavailable(t *testing.T) {
	bp := BottomPanel{storeErr: ErrStoreUnavailable}
	assert.Contains(t, bp.View(80), "DB: недоступна")

	assert.NotContains(t, BottomPanel{}.View(80), "DB: недоступна")
}

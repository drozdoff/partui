package app

import (
	"path/filepath"
	"testing"
	"time"

	"partui/internal/config"
	"partui/internal/index"

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

func TestModel_DegradedMode_RefreshStats(t *testing.T) {
	m := newTestModel()
	m.app.store = newNoopStore(m.app.logger)

	m.refreshStats()
	assert.ErrorIs(t, m.bottomPanel.storeErr, ErrStoreUnavailable)
	assert.Equal(t, 0, m.bottomPanel.totalCount)
	assert.Equal(t, 0, m.bottomPanel.processingCount)
}

// Item 3.2: the panel statistics are computed in a single All() pass
func TestModel_RefreshStats_CountsStatuses(t *testing.T) {
	m := newTestModel()
	fixed := time.Unix(1700000000, 0)
	st := newFakeStore()
	require.NoError(t, st.Put("/d", "ok.par2", index.StatusOK, fixed))
	require.NoError(t, st.Put("/d", "proc.par2", index.StatusProcessing, fixed))
	require.NoError(t, st.Put("/d", "proc2.par2", index.StatusProcessing, fixed))
	require.NoError(t, st.Put("/d", "bad.par2", index.StatusDamaged, fixed))
	m.app.store = st

	m.refreshStats()

	assert.Equal(t, 4, m.bottomPanel.totalCount)
	assert.Equal(t, 2, m.bottomPanel.processingCount)
	require.Len(t, m.bottomPanel.damagedItems, 1)
	assert.Equal(t, "bad.par2", m.bottomPanel.damagedItems[0].par2Filename)
	assert.Equal(t, "/d", m.bottomPanel.damagedItems[0].dir)
	assert.NoError(t, m.bottomPanel.storeErr)
}

func TestBottomPanelView_StoreUnavailable(t *testing.T) {
	bp := BottomPanel{storeErr: ErrStoreUnavailable}
	assert.Contains(t, bp.View(80), "DB: недоступна")

	assert.NotContains(t, BottomPanel{}.View(80), "DB: недоступна")
}

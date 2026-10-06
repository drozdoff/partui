package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"partui/internal/config"
	"partui/internal/index"
	"partui/internal/par2"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newIntegrationApp wires the application exactly like run() does: a real
// bbolt store inside dir, the real gopar runner and a running headless UI
// loop, so the worker completion messages are handled like in the real app
// (plan item 5.2). The returned stop function quits the loop and waits for
// it; because the event loop is FIFO, every message sent before stop is
// fully handled when stop returns — the assertions after stop are
// deterministic, not racy.
func newIntegrationApp(t *testing.T, dir string) (*App, func()) {
	t.Helper()

	app := NewApp(config.Defaults(), discardLogger())
	st, err := newBboltStore(filepath.Join(dir, "indexes.db"), app.ctx, app.logger)
	require.NoError(t, err)
	app.store = st

	m := NewModel(app)
	p := tea.NewProgram(m,
		tea.WithContext(app.ctx),
		tea.WithoutRenderer(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
	)
	app.program = p

	done := make(chan struct{})
	go func() {
		_, _ = p.Run()
		close(done)
	}()

	stop := func() {
		p.Quit()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// the loop must never hang the test; cancel as a last resort
			app.cancel()
			<-done
		}
	}
	t.Cleanup(func() {
		stop()
		app.Shutdown()
	})

	return app, stop
}

// writeDataFile creates a data file of the given size filled with fillByte
func writeDataFile(t *testing.T, dir string, fillByte byte, size int) string {
	t.Helper()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{fillByte}, size), 0o644))
	return path
}

// The full round trip: create a PAR2 with the real gopar, read the created
// archive back with ListPar2Filenames and check that the completion handler
// stored the index record under the .par2 name.
func TestIntegration_CreateVerifyRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	data := writeDataFile(t, tmp, 'x', 64_000)

	// the worker body is called directly, not via launch: it is
	// synchronous and hands its result to the UI loop via Send
	app.createParFiles(tmp, "test.txt", 64_000)

	parPath := data + ".par2"
	require.FileExists(t, parPath)

	names, err := par2.ListPar2Filenames(parPath)
	require.NoError(t, err)
	assert.Contains(t, names, "test.txt")

	stop()

	desc, err := app.store.Get(tmp, "test.txt.par2")
	require.NoError(t, err)
	require.NotNil(t, desc, "the finished message must store the index record")
	assert.Equal(t, index.StatusOK, desc.Status)

	// a freshly created archive must verify as OK
	app.verifyParFiles(tmp, indexListItem{par2Filename: "test.txt.par2", dir: tmp})

	stop()

	desc, err = app.store.Get(tmp, "test.txt.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusOK, desc.Status)
}

// Corrupting the data file must surface as StatusDamaged in the store.
func TestIntegration_VerifyDamaged(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	data := writeDataFile(t, tmp, 'x', 64_000)
	app.createParFiles(tmp, "test.txt", 64_000)

	// flip every byte in place, keeping the size
	require.NoError(t, os.WriteFile(data, bytes.Repeat([]byte{'y'}, 64_000), 0o644))

	app.verifyParFiles(tmp, indexListItem{par2Filename: "test.txt.par2", dir: tmp})

	stop()

	desc, err := app.store.Get(tmp, "test.txt.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusDamaged, desc.Status)
}

// A directory that disappeared (offline media) yields StatusMissing and
// keeps the old LastVerified — the index was not actually verified.
func TestIntegration_VerifyMissingDir(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	missing := filepath.Join(tmp, "gone")
	require.NoError(t, os.Mkdir(missing, 0o755))

	old := time.Now().Add(-24 * time.Hour)
	require.NoError(t, app.store.Put(missing, "a.par2", index.StatusOK, old))
	require.NoError(t, os.RemoveAll(missing))

	app.verifyParFiles(missing, indexListItem{par2Filename: "a.par2", dir: missing})

	stop()

	desc, err := app.store.Get(missing, "a.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusMissing, desc.Status)
	assert.WithinDuration(t, old, desc.LastVerified, time.Second,
		"a missing-directory check must not touch LastVerified")
}

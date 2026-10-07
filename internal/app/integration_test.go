package app

import (
	"bytes"
	"context"
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

// The ASCII round trip with a non-ASCII name: create, read back with
// ListPar2Filenames, store the record, verify — all with the exact byte
// sequence of the name (plans/utf8_filenames.md §5).
func TestIntegration_CreateVerifyRoundtrip_UTF8(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	title := "отчёт №5.bin"
	data := filepath.Join(tmp, title)
	require.NoError(t, os.WriteFile(data, bytes.Repeat([]byte{'x'}, 64_000), 0o644))

	app.createParFiles(tmp, title, 64_000)

	parPath := data + ".par2"
	require.FileExists(t, parPath)

	names, err := par2.ListPar2Filenames(parPath)
	require.NoError(t, err)
	assert.Contains(t, names, title, "the archive must list the exact name")

	stop()

	desc, err := app.store.Get(tmp, title+".par2")
	require.NoError(t, err)
	require.NotNil(t, desc, "the finished message must store the index record")
	assert.Equal(t, index.StatusOK, desc.Status)

	app.verifyParFiles(tmp, indexListItem{par2Filename: title + ".par2", dir: tmp})

	stop()

	desc, err = app.store.Get(tmp, title+".par2")
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

// A cancellation in the middle of a verification must not flip the index to
// Unverifiable: the worker exits silently — symmetric with a shutdown while
// the task is queued — and the status stays processing
// (plans/streaming_file.md §5.4.3).
func TestIntegration_VerifyCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	require.NoError(t, app.store.Put(tmp, "a.par2", index.StatusProcessing, app.now()))

	// a runner whose verify blocks until its context is cancelled — the
	// deterministic stand-in for "cancelled mid-file"
	fp := newFakePar2()
	fp.verifyFunc = func(ctx context.Context, parPath string) (par2.VerifyResult, error) {
		<-ctx.Done()
		return par2.VerifyResult{}, ctx.Err()
	}
	app.par2 = fp

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.verifyParFiles(tmp, indexListItem{par2Filename: "a.par2", dir: tmp})
	}()
	app.cancel()
	<-done

	stop()

	desc, err := app.store.Get(tmp, "a.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusProcessing, desc.Status,
		"a cancelled verification must not write a status")
}

// A cancellation in the middle of a creation must not report a failed
// create — the worker exits silently, symmetric with the cancelled verify
// (plans/streaming_file.md §6.6.2). The removal of partial volume files is
// pinned at the runner level, by TestCreateStream_Cancel.
func TestIntegration_CreateCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	require.NoError(t, app.store.Put(tmp, "a.txt.par2", index.StatusProcessing, app.now()))

	fp := newFakePar2()
	fp.createFunc = func(ctx context.Context, parPath string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	app.par2 = fp

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.createParFiles(tmp, "a.txt", 1000)
	}()
	app.cancel()
	<-done

	stop()

	desc, err := app.store.Get(tmp, "a.txt.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusProcessing, desc.Status,
		"a cancelled creation must not write a status")
}

// Creating a PAR2 for an empty data file used to panic in gopar's RS coder
// and take the whole app down (plans/streaming_file.md §6.6.3); the
// streaming create must surface an ordinary error instead, through the
// regular failure handler.
func TestIntegration_CreateEmptyFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	tmp := t.TempDir()
	app, stop := newIntegrationApp(t, tmp)

	data := writeDataFile(t, tmp, 'x', 0)

	// must not panic; the error goes through handleParCreateError
	app.createParFiles(tmp, "test.txt", 0)

	stop()

	// the source file survives, no archive appears
	require.FileExists(t, data)
	_, statErr := os.Stat(data + ".par2")
	assert.True(t, os.IsNotExist(statErr), "a failed create must leave no archive")
}

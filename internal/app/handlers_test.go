package app

import (
	"errors"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Handler-level tests (plan item 5.1): every completion message handler
// must update the list entries, the store and the error line consistently.

func TestUpdate_ParCreateFinished_UpdatesItems(t *testing.T) {
	m := newTestModel()
	m.currentDir = "/tmp/x"
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", processing: true}})
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.txt.par2", dir: "/tmp/x", status: index.StatusProcessing},
	})

	_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})

	fit := m.fileList.Items()[0].(fileListItem)
	assert.False(t, fit.processing)
	assert.True(t, fit.archive)

	iit := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusOK, iit.status)
	// the description must show the new status, not a stale "Processing"
	assert.Equal(t, "/tmp/x | OK", iit.desc)
}

// Regression test: parCreateFinishedMsg must store the index record under
// the .par2 file name, not the source file name.
func TestUpdate_ParCreateFinished_StoresPar2NameInStore(t *testing.T) {
	app := newTestApp()
	m := NewModel(app)
	m.currentDir = "/tmp/x"

	_, _ = m.Update(parCreateFinishedMsg{dir: "/tmp/x", title: "a.txt"})

	desc, err := app.store.Get("/tmp/x", "a.txt.par2")
	assert.NoError(t, err)
	require.NotNil(t, desc, "index record must be stored under the .par2 name")
	assert.Equal(t, index.StatusOK, desc.Status)

	junk, err := app.store.Get("/tmp/x", "a.txt")
	assert.NoError(t, err)
	assert.Nil(t, junk, "no record must be stored under the source file name")
}

func TestUpdate_ParCreateError_ReleasesFileAndMarksDamaged(t *testing.T) {
	m := newTestModel()
	m.currentDir = "/tmp/x"
	m.fileList.SetItems([]list.Item{fileListItem{title: "a.txt", processing: true}})
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.txt.par2", dir: "/tmp/x", status: index.StatusProcessing},
	})

	_, _ = m.Update(parCreateErrorMsg{dir: "/tmp/x", title: "a.txt", err: errors.New("disk full")})

	fit := m.fileList.Items()[0].(fileListItem)
	assert.False(t, fit.processing, "the file must be released from processing")

	iit := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusDamaged, iit.status)

	// reportable level: the failure is visible in the UI (plan item 4.1)
	assert.Contains(t, m.errText, "PAR2 creation failed")
	assert.Contains(t, m.View(), "PAR2 creation failed")
}

// Item 5.1: StatusMissing means the index was not actually verified —
// LastVerified must survive unchanged, both in the store and in the list.
func TestUpdate_VerifyFinished_Missing_KeepsLastVerified(t *testing.T) {
	fixed := time.Unix(1700000000, 0)
	m := newTestModel()
	m.app.clock = func() time.Time { return fixed }
	m.currentDir = "/tmp/x"

	old := fixed.Add(-24 * time.Hour)
	require.NoError(t, m.app.store.Put("/tmp/x", "a.par2", index.StatusOK, old))
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.par2", dir: "/tmp/x", status: index.StatusOK, lastVerified: old},
	})

	_, _ = m.Update(indexVerifyFinishedMsg{
		dir:          "/tmp/x",
		par2Filename: "a.par2",
		status:       index.StatusMissing,
	})

	desc, err := m.app.store.Get("/tmp/x", "a.par2")
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.Equal(t, index.StatusMissing, desc.Status)
	assert.WithinDuration(t, old, desc.LastVerified, time.Second, "LastVerified must not change")

	it := m.indexList.Items()[0].(indexListItem)
	assert.Equal(t, index.StatusMissing, it.status)
	assert.WithinDuration(t, old, it.lastVerified, time.Second, "the list entry keeps the old timestamp too")

	// a missing directory is an outcome, not an operation failure: no error line
	assert.Empty(t, m.errText)
}

// Regression: a verification result for a name that cannot be a PAR2 file
// (legacy records written by the pre-refactoring binary) is shown in the
// list but never persisted.
func TestUpdate_VerifyFinished_NonPar2Name_NotStored(t *testing.T) {
	m := newTestModel()
	m.currentDir = "/tmp/x"

	_, _ = m.Update(indexVerifyFinishedMsg{
		dir:          "/tmp/x",
		par2Filename: "style.css",
		status:       index.StatusUnverifiable,
		err:          errors.New("parPath must have a .par2 extension"),
	})

	desc, err := m.app.store.Get("/tmp/x", "style.css")
	assert.NoError(t, err)
	assert.Nil(t, desc, "no record must be stored for a non-.par2 name")
}

// A failed verification is surfaced to the user, not only to the log
func TestUpdate_VerifyFinished_ErrorShown(t *testing.T) {
	m := newTestModel()
	fixed := time.Unix(1700000000, 0)
	m.app.clock = func() time.Time { return fixed }
	m.currentDir = "/tmp/x"

	_, _ = m.Update(indexVerifyFinishedMsg{
		dir:          "/tmp/x",
		par2Filename: "a.txt.par2",
		status:       index.StatusUnverifiable,
		err:          errors.New("unexpected EOF"),
	})

	assert.Contains(t, m.errText, "PAR2 verification failed")
	assert.Contains(t, m.errText, "unexpected EOF")
}

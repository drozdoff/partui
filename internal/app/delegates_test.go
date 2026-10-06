package app

import (
	"bytes"
	"strings"
	"testing"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
)

// Render-surface tests (plan item 5.1): the delegates must render every
// item flavor without panicking and produce non-empty output.

func TestFileListItemDelegate_Render(t *testing.T) {
	m := newTestModel()
	d := newFileListItemDelegate(m)

	tests := []struct {
		name     string
		focus    ListFocus
		item     fileListItem
		atCursor bool
	}{
		{"plain file", FocusFile, fileListItem{title: "a.txt", desc: "7 bytes"}, false},
		{"file under the cursor", FocusFile, fileListItem{title: "a.txt", desc: "7 bytes"}, true},
		{"directory under the cursor", FocusFile, fileListItem{title: "sub/", isDir: true}, true},
		{"ticked file", FocusFile, fileListItem{title: "a.txt", desc: "7 bytes", selected: true}, false},
		{"processing file", FocusFile, fileListItem{title: "a.txt", desc: "7 bytes", processing: true}, false},
		{"archived file", FocusFile, fileListItem{title: "a.txt", desc: "7 bytes", archive: true}, false},
		{"long title truncates", FocusFile, fileListItem{title: strings.Repeat("а", 60), desc: "7 bytes"}, false},
		{"title and desc overflow", FocusFile, fileListItem{title: strings.Repeat("а", 30), desc: strings.Repeat("b", 30)}, false},
		{"index focus ignores selection style", FocusIndex, fileListItem{title: "a.txt", desc: "7 bytes"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.focus = tc.focus
			lm := list.New([]list.Item{tc.item}, d, 0, 0)
			lm.SetSize(40, 10)

			var buf bytes.Buffer
			assert.NotPanics(t, func() {
				d.Render(&buf, lm, 0, tc.item)
			})
			assert.NotEmpty(t, buf.String())
		})
	}

	// an item of an unexpected type renders nothing
	m.focus = FocusFile
	var buf bytes.Buffer
	d.Render(&buf, list.New(nil, d, 0, 0), 0, stubItem{})
	assert.Empty(t, buf.String())
}

func TestIndexListItemDelegate_Render(t *testing.T) {
	m := newTestModel()
	d := newIndexListItemDelegate(m)

	tests := []struct {
		name     string
		focus    ListFocus
		status   index.Status
		atCursor bool
	}{
		{"ok", FocusIndex, index.StatusOK, false},
		{"processing", FocusIndex, index.StatusProcessing, false},
		{"damaged", FocusIndex, index.StatusDamaged, false},
		{"unverifiable falls back to the normal style", FocusIndex, index.StatusUnverifiable, false},
		{"missing falls back to the normal style", FocusIndex, index.StatusMissing, false},
		{"selected", FocusIndex, index.StatusOK, true},
		{"file focus ignores selection style", FocusFile, index.StatusOK, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.focus = tc.focus
			item := indexListItem{
				par2Filename: "a.txt.par2",
				dir:          "/d",
				desc:         formatIndexItemDesc(indexListItem{dir: "/d", status: tc.status}, false),
				status:       tc.status,
			}
			lm := list.New([]list.Item{item}, d, 0, 0)
			lm.SetSize(40, 10)

			var buf bytes.Buffer
			assert.NotPanics(t, func() {
				d.Render(&buf, lm, 0, item)
			})
			assert.Contains(t, buf.String(), "a.txt.par2")
		})
	}

	// an item of an unexpected type renders nothing
	var buf bytes.Buffer
	d.Render(&buf, list.New(nil, d, 0, 0), 0, stubItem{})
	assert.Empty(t, buf.String())
}

// The list.Model interface implementation feeds both the delegates and the
// filtering
func TestItems_ListInterface(t *testing.T) {
	fi := fileListItem{title: "a.txt", desc: "7 bytes"}
	assert.Equal(t, "a.txt", fi.Title())
	assert.Equal(t, "7 bytes", fi.Description())
	assert.Equal(t, "a.txt", fi.FilterValue())

	ii := indexListItem{par2Filename: "a.par2", desc: "/d | OK"}
	assert.Equal(t, "a.par2", ii.Title())
	assert.Equal(t, "/d | OK", ii.Description())
	assert.Equal(t, "a.par2", ii.FilterValue())
}

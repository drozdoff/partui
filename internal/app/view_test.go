package app

// Render-surface tests for the pieces introduced by the UI revamp plan:
// the relative time formatter, the index row meta, the header line and the
// footer status line.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatRelativeTime(t *testing.T) {
	// fixed now keeps the table deterministic
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"just now", 0, "just now"},
		{"under a minute", 42 * time.Second, "just now"},
		{"future clock skew", -5 * time.Minute, "just now"},
		{"minutes", 5 * time.Minute, "5 min ago"},
		{"just under an hour", 59 * time.Minute, "59 min ago"},
		{"an hour", time.Hour, "1 h ago"},
		{"hours", 3 * time.Hour, "3 h ago"},
		{"just under a day", 23*time.Hour + 59*time.Minute, "23 h ago"},
		{"a day", 24 * time.Hour, "yesterday"},
		{"two days", 48 * time.Hour, "2 d ago"},
		{"six days", 6 * 24 * time.Hour, "6 d ago"},
		{"a week falls back to the date", 7 * 24 * time.Hour, "29.09.2026"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatRelativeTime(now.Add(-tc.ago), now))
		})
	}
}

func TestFormatIndexMeta(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	verified := now.Add(-2 * time.Hour)
	item := indexListItem{par2Filename: "a.par2", dir: "/d", lastVerified: verified}

	// the collapsed mode shows only the time: the directory is already in
	// the header, every index of the list lives there
	assert.Equal(t, "2 h ago", formatIndexMeta(item, false, now))

	// the expanded mode prefixes the directory
	assert.Equal(t, "/d · 2 h ago", formatIndexMeta(item, true, now))

	// a fresh placeholder has no verification time
	fresh := indexListItem{par2Filename: "b.par2", dir: "/d"}
	assert.Empty(t, formatIndexMeta(fresh, false, now))
	assert.Equal(t, "/d", formatIndexMeta(fresh, true, now))
}

// The header line puts the full path on the left and the counters on the
// right; on a narrow terminal the path is truncated on the left, the
// counters stay whole.
func TestModel_HeaderLine(t *testing.T) {
	m := newTestModel()
	m.currentDir = "/home/user/backups"
	m.header = Header{totalCount: 42, processingCount: 3}
	m.width = 80

	line := m.headerLine(80)
	assert.True(t, strings.HasPrefix(line, "/home/user"), "expected the full path, got %q", line)
	assert.Contains(t, line, "42 in index")
	assert.Contains(t, line, "3 in progress")
	assert.Contains(t, line, "0 damaged")
	assert.LessOrEqual(t, lipgloss.Width(line), 80)

	// a narrow line: the path is cut on the left with an ellipsis
	line = m.headerLine(50)
	assert.True(t, strings.HasPrefix(line, "…"), "expected a left ellipsis, got %q", line)
	assert.Contains(t, line, "42 in index")
	assert.LessOrEqual(t, lipgloss.Width(line), 50)

	// an extreme narrow line, the counters alone are wider: only the
	// counters remain
	line = m.headerLine(20)
	assert.Equal(t, m.header.counters(), line)
}

// The status line priority: error, then activity, then the idle marker.
func TestModel_StatusLine(t *testing.T) {
	m := newTestModel()
	m.width = 80

	// idle
	assert.Contains(t, m.statusLine(), "ready")

	// a task runs: spinner + kind, no error shown
	m.verifying = 2
	assert.Contains(t, m.statusLine(), "verifying")
	assert.NotContains(t, m.statusLine(), "ready")

	// a creation only: the label follows the task kind
	m.verifying = 0
	m.creating = 1
	assert.Contains(t, m.statusLine(), "creating")

	// both kinds: the generic label
	m.verifying = 1
	assert.Contains(t, m.statusLine(), "working")

	// an active error displaces the activity line
	m.errText = "change directory: permission denied"
	m.errUntil = m.app.now().Add(time.Minute)
	assert.Contains(t, m.statusLine(), "change directory")
	assert.NotContains(t, m.statusLine(), "working")

	// an expired error is gone
	m.errUntil = m.app.now().Add(-time.Second)
	assert.NotContains(t, m.statusLine(), "change directory")
	assert.Contains(t, m.statusLine(), "working")
}

// The worker-slot indicator: one cell per configured slot, x = busy, · free.
func TestModel_SlotIndicator(t *testing.T) {
	m := newTestModel()
	m.app.cfg.MaxWorkers = 3
	m.verifying = 2

	out := m.slotIndicator()
	assert.Equal(t, 2, strings.Count(out, "x"))
	assert.Equal(t, 1, strings.Count(out, "·"))
	assert.True(t, strings.HasPrefix(out, "[") && strings.HasSuffix(out, "]"))

	// busy caps at the slot count: tasks queued beyond the slots are
	// invisible here
	m.verifying = 5
	assert.Equal(t, 3, strings.Count(m.slotIndicator(), "x"))
	assert.NotContains(t, m.slotIndicator(), "·")

	// without a configured limit, one cell per running task
	m.app.cfg.MaxWorkers = 0
	m.verifying = 2
	m.creating = 1
	out = m.slotIndicator()
	assert.Equal(t, 3, strings.Count(out, "x"))
	assert.Equal(t, 0, strings.Count(out, "·"))
}

// The footer status line reacts to the task counters only through their
// launch bookkeeping: verifySelected increments, handleVerifyFinished
// decrements back to zero, and the spinner chain starts once.
func TestModel_TaskCounters_Lifecycle(t *testing.T) {
	m := newTestModel()
	m.app.program = tea.NewProgram(m, tea.WithContext(m.app.ctx))
	m.focus = FocusIndex
	dir := t.TempDir() // must exist: verify checks the directory first
	m.indexList.SetItems([]list.Item{
		indexListItem{par2Filename: "a.par2", dir: dir, status: index.StatusOK},
	})

	assert.False(t, m.jobsActive())
	cmd := m.verifySelected()
	assert.Equal(t, 1, m.verifying)
	assert.NotNil(t, cmd, "the first launch must start the spinner chain")

	// a second launch must not stack another spinner chain
	assert.Nil(t, m.spinCmd())

	// drain the fake runner so the worker can report, then cancel the
	// context so the goroutine parked on program.Send exits
	runner := m.app.par2.(*fakePar2)
	select {
	case <-runner.verifiedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Par2Runner.Verify was not called")
	}
	m.app.cancel()
	m.app.wg.Wait()

	_ = m.handleVerifyFinished(indexVerifyFinishedMsg{
		dir: dir, par2Filename: "a.par2", status: index.StatusOK,
	})
	assert.Equal(t, 0, m.verifying)
	assert.False(t, m.jobsActive())

	// taskDone floors at zero: finishing without a launch must not drive
	// the counter negative
	taskDone(&m.creating)
	assert.Equal(t, 0, m.creating)
}

// UI-4 acceptance: one-line rows — the status carries the marker, the name
// and the meta are plain text. Colors are stripped in tests (no color
// profile), so only the composition can be asserted here.
func TestIndexListItemDelegate_StatusIsMarkerOnly(t *testing.T) {
	m := newTestModel()
	d := newIndexListItemDelegate(m)
	item := indexListItem{par2Filename: "a.par2", dir: "/d", desc: "2 h ago", status: index.StatusOK}

	lm := list.New([]list.Item{item}, d, 0, 0)
	lm.SetSize(40, 10)

	var buf strings.Builder
	d.Render(&buf, lm, 0, item)

	out := buf.String()
	assert.True(t, strings.HasPrefix(out, "   ● a.par2"), "got %q", out)
	assert.True(t, strings.HasSuffix(out, "2 h ago"), "got %q", out)
	assert.NotContains(t, out, "|", "no dividers")
}

// The cursor row of the index list renders as a bar spanning the whole list
// width; there is no separate cursor glyph.
func TestIndexListItemDelegate_CursorBar(t *testing.T) {
	m := newTestModel()
	m.focus = FocusIndex
	d := newIndexListItemDelegate(m)
	item := indexListItem{par2Filename: "a.par2", dir: "/d", status: index.StatusOK}

	lm := list.New([]list.Item{item}, d, 0, 0)
	lm.SetSize(40, 10)

	var buf strings.Builder
	d.Render(&buf, lm, 0, item)

	assert.Equal(t, 40, lipgloss.Width(buf.String()))
	assert.NotContains(t, buf.String(), "▸")
}

// Regression: the row of the index list is composed of separately styled
// segments, and every style ends with an ANSI reset that wipes a background
// painted by an outer wrap. With a real color profile the bar background
// must be re-emitted around every piece of the row, so the bar spans the
// whole line and not only its first and last characters.
func TestIndexListItemDelegate_CursorBarSpansRow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := newTestModel()
	m.focus = FocusIndex
	d := newIndexListItemDelegate(m)
	item := indexListItem{par2Filename: "a.par2", dir: "/d", desc: "2 h ago", status: index.StatusOK}

	lm := list.New([]list.Item{item}, d, 0, 0)
	lm.SetSize(40, 10)

	var buf strings.Builder
	d.Render(&buf, lm, 0, item)
	out := buf.String()

	// resolve the bar background color the way lipgloss renders it
	ref := lipgloss.NewStyle().Background(colCursor).Render(" ")
	var bgParams string
	for _, seq := range ansiSeqRe.FindAllString(ref, -1) {
		if params := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m"); strings.Contains(params, "48;2;") {
			bgParams = params
		}
	}
	require.NotEmpty(t, bgParams, "no background sequence in %q", ref)

	// padding + marker + body: the background must be established at least
	// three times (lipgloss may combine fg+bg into one SGR), otherwise some
	// part of the row loses the bar
	assert.GreaterOrEqual(t, strings.Count(out, bgParams), 3, "bar must span the row: %q", out)
	assert.Equal(t, 40, lipgloss.Width(out))
}

var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Files and directories share one level: rows start at column 0 without
// indentation.
func TestFileListItemDelegate_FlushLeft(t *testing.T) {
	m := newTestModel()
	d := newFileListItemDelegate(m)
	items := []list.Item{
		fileListItem{title: "sub/", isDir: true, desc: "64 bytes"},
		fileListItem{title: "a.txt", desc: "17 bytes"},
	}
	lm := list.New(items, d, 0, 0)
	lm.SetSize(40, 10)

	var dirBuf, fileBuf strings.Builder
	d.Render(&dirBuf, lm, 0, items[0])
	d.Render(&fileBuf, lm, 1, items[1])

	assert.True(t, strings.HasPrefix(dirBuf.String(), "sub/"), "got %q", dirBuf.String())
	assert.True(t, strings.HasPrefix(fileBuf.String(), "a.txt"), "got %q", fileBuf.String())
}

// Each list pane is padded one column on both sides: the lists must be
// sized to leave room for the padding, so the total stays within the
// terminal width.
func TestApplyLayout_PanePadding(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 24

	m.applyLayout()
	assert.Equal(t, 48, m.fileList.Width())
	assert.Equal(t, 48, m.indexList.Width())

	m.indexListExpanded = true
	m.applyLayout()
	assert.Equal(t, 98, m.indexList.Width())
}

package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"partui/internal/index"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Init hands the background load of the start directory's archives to the
// program along with the window title
func (m *Model) Init() tea.Cmd {
	if m.startupCmd != nil {
		return tea.Batch(m.startupCmd, tea.SetWindowTitle("Partui"))
	}
	return tea.SetWindowTitle("Partui")
}

// Header displays the index statistics rendered on the top line of the UI
// (plan UI_revamp, step UI-2)
type Header struct {
	totalCount      int
	processingCount int
	okCount         int
	damagedItems    []DamagedIndex

	// storeErr is non-nil when index statistics are unavailable
	// (degraded mode or a failed database query)
	storeErr error
}

// DamagedIndex represents a damaged index file
type DamagedIndex struct {
	par2Filename string
	dir          string
}

// headerHeight is the fixed height of the header: the path/counters line
// plus the dim separator line. applyLayout subtracts it from the terminal
// height, so it must match what renderHeader emits.
const headerHeight = 2

// refreshStats recomputes the header statistics in a single store
// pass: the total, the processing count and the damaged list all come from
// one All() query instead of the previous three queries (plan item 7.1).
// A failed query (or degraded mode) is reported via Header.storeErr
// instead of silently showing zeros.
func (m *Model) refreshStats() {
	all, err := m.app.store.All()
	if err != nil {
		m.app.logStoreError("failed to load indexes", err)
		m.header = Header{storeErr: err}
		return
	}

	var damaged []DamagedIndex
	processingCount := 0
	okCount := 0
	for _, idx := range all {
		switch idx.Status {
		case index.StatusProcessing:
			processingCount++
		case index.StatusOK:
			okCount++
		case index.StatusDamaged:
			damaged = append(damaged, DamagedIndex{
				par2Filename: idx.Filename,
				dir:          idx.Dir,
			})
		}
	}

	m.header = Header{
		totalCount:      len(all),
		processingCount: processingCount,
		okCount:         okCount,
		damagedItems:    damaged,
	}
}

// counters renders the right part of the header: three "N label" counters
// separated by spaces, no dividers. Zero counts render dim; in degraded
// mode a red note replaces the counters.
func (h Header) counters() string {
	if h.storeErr != nil {
		return styleDamaged.Render("db unavailable")
	}

	inIndex := styleBold.Render(fmt.Sprintf("%d", h.totalCount)) + styleDim.Render(" in index")

	inProgress := styleDim.Render(fmt.Sprintf("%d", h.processingCount) + " in progress")
	if h.processingCount > 0 {
		inProgress = styleWork.Bold(true).Render(fmt.Sprintf("%d", h.processingCount)) +
			styleDim.Render(" in progress")
	}

	damaged := styleDim.Render(fmt.Sprintf("%d", len(h.damagedItems)) + " damaged")
	if len(h.damagedItems) > 0 {
		damaged = styleDamaged.Bold(true).Render(fmt.Sprintf("%d", len(h.damagedItems))) +
			styleDim.Render(" damaged")
	}

	return inIndex + "   " + inProgress + "   " + damaged
}

// handleWindowSize stores the new size and applies it to the lists
func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) tea.Cmd {
	m.width = msg.Width
	m.height = msg.Height

	m.applyLayout()

	var cmd tea.Cmd
	m.indexList.Update(msg)
	m.fileList, cmd = m.fileList.Update(msg)
	return cmd
}

// applyLayout sizes the lists for the current mode. It runs on window resize,
// whenever the expanded mode is toggled and whenever the footer changes
// height (the delete dialog opens or closes), so that View stays free of
// state changes. Each list is two columns narrower than its pane: paneStyle
// pads it on both sides.
func (m *Model) applyLayout() {
	avail := max(m.height-headerHeight-m.footerHeight(), 0)

	if m.indexListExpanded {
		m.indexList.SetHeight(avail)
		m.indexList.SetWidth(max(m.width-2, 0))
		return
	}

	w := max(m.width/2-2, 0)
	m.fileList.SetHeight(avail)
	m.fileList.SetWidth(w)
	m.indexList.SetHeight(avail)
	m.indexList.SetWidth(w)
}

// paneStyle pads each list one column from the screen edges and from the
// other pane, so the panels do not stick together
var paneStyle = lipgloss.NewStyle().Padding(0, 1)

func (m *Model) View() string {
	// Expanded mode - full-screen index list under the header
	if m.indexListExpanded {
		return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), paneStyle.Render(m.indexList.View()), m.renderFooter())
	}

	lists := lipgloss.JoinHorizontal(lipgloss.Top, paneStyle.Render(m.fileList.View()), paneStyle.Render(m.indexList.View()))

	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), lists, m.renderFooter())
}

// renderHeader renders the path/counters line and the dim separator under it
func (m *Model) renderHeader() string {
	separator := ""
	if m.width > 0 {
		separator = styleDim.Render(strings.Repeat("─", m.width))
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.headerLine(m.width), separator)
}

// headerLine renders the top line: the full current directory on the left,
// the index counters on the right. On narrow terminals the path yields
// first: it is truncated on the left with an ellipsis, the counters always
// stay whole.
func (m *Model) headerLine(width int) string {
	counters := m.header.counters()
	avail := width - lipgloss.Width(counters) - 1
	if avail <= 0 {
		return counters
	}

	path := m.currentDir
	if rp := utf8.RuneCountInString(path); rp > avail {
		if avail <= 1 {
			path = ""
		} else {
			path = "…" + string([]rune(path)[rp-avail+1:])
		}
	}

	pad := max(avail-utf8.RuneCountInString(path), 1)
	return path + strings.Repeat(" ", pad) + counters
}

// footerHeight is the current footer height: the key hints (or the delete
// dialog, which is taller) plus the status line. applyLayout subtracts it
// from the terminal height, so it must match what renderFooter emits.
func (m *Model) footerHeight() int {
	if m.confirm != nil {
		return confirmHeight + 1
	}
	return 2
}

// renderFooter renders the two footer zones (plan UI_revamp, step UI-3):
// the context line — key hints or the open delete dialog — and the status
// line below it
func (m *Model) renderFooter() string {
	return lipgloss.JoinVertical(lipgloss.Left, m.contextLine(), m.statusLine())
}

// contextLine is the delete dialog while it is open, the key hints otherwise
func (m *Model) contextLine() string {
	if m.confirm != nil {
		return m.confirm.View(m.width)
	}
	return m.helpLine()
}

// helpLine shows the keys of the focused list, dim
func (m *Model) helpLine() string {
	switch {
	case m.indexListExpanded:
		return styleDim.Render("esc back · v verify · r repair · d delete · q quit")
	case m.focus == FocusIndex:
		return styleDim.Render("tab files · v verify · r repair · d delete · q quit")
	default:
		return styleDim.Render("tab indexes · space select · c create · enter open · q quit")
	}
}

// statusLine shows the active error, else the background task activity —
// the spinner plus the worker-slot indicator — else a dim idle marker.
// The error is transient (errorVisibleFor), so it may temporarily displace
// the activity line.
func (m *Model) statusLine() string {
	if m.errText != "" && m.app.now().Before(m.errUntil) {
		return styleDamaged.Render(truncateRunes(m.errText, m.width))
	}
	if m.jobsActive() {
		return m.activityLine()
	}
	return styleDim.Render("ready")
}

// activityLine is the spinner, the kind of the running tasks and the
// worker-slot indicator, e.g. "⣾ verifying  [x x ·]"
func (m *Model) activityLine() string {
	label := "working"
	switch {
	case m.creating == 0 && m.repairing == 0:
		label = "verifying"
	case m.verifying == 0 && m.repairing == 0:
		label = "creating"
	case m.verifying == 0 && m.creating == 0:
		label = "repairing"
	}
	return m.spinner.View() + " " + styleWork.Render(label) + "  " + m.slotIndicator()
}

// slotIndicator renders [x x ·]: one cell per worker slot — x busy, · free.
// Tasks beyond the first MaxWorkers sit queued for a slot, so the busy
// count simply caps at the slot count. Without a configured limit the
// indicator falls back to one cell per running task.
func (m *Model) slotIndicator() string {
	busy := m.verifying + m.creating + m.repairing
	slots := m.app.cfg.MaxWorkers
	if slots <= 0 {
		slots = busy
	}

	cells := make([]string, 0, slots)
	for i := 0; i < slots; i++ {
		if i < busy {
			cells = append(cells, styleWork.Render("x"))
		} else {
			cells = append(cells, styleDim.Render("·"))
		}
	}
	return "[" + strings.Join(cells, " ") + "]"
}

// truncateRunes cuts s to at most width columns, appending … when cut
func truncateRunes(s string, width int) string {
	runes := []rune(s)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

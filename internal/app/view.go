package app

import (
	"fmt"
	"partui/internal/index"
	"path/filepath"
	"strings"

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

// BottomPanel displays index statistics at the bottom of the UI
type BottomPanel struct {
	totalCount      int
	processingCount int
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

// refreshStats recomputes the bottom panel statistics in a single store
// pass: the total, the processing count and the damaged list all come from
// one All() query instead of the previous three queries (plan item 7.1).
// A failed query (or degraded mode) is reported via BottomPanel.storeErr
// instead of silently showing zeros.
func (m *Model) refreshStats() {
	all, err := m.app.store.All()
	if err != nil {
		m.app.logStoreError("failed to load indexes", err)
		m.bottomPanel = BottomPanel{storeErr: err}
		return
	}

	var damaged []DamagedIndex
	processingCount := 0
	for _, idx := range all {
		switch idx.Status {
		case index.StatusProcessing:
			processingCount++
		case index.StatusDamaged:
			damaged = append(damaged, DamagedIndex{
				par2Filename: idx.Filename,
				dir:          idx.Dir,
			})
		}
	}

	m.bottomPanel = BottomPanel{
		totalCount:      len(all),
		processingCount: processingCount,
		damagedItems:    damaged,
	}
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

// applyLayout sizes the lists for the current mode. It runs on window resize
// and whenever the expanded mode is toggled, so that View stays free of
// state changes.
func (m *Model) applyLayout() {
	if m.indexListExpanded {
		m.indexList.SetHeight(m.height)
		m.indexList.SetWidth(m.width)
		return
	}

	w := m.width / 2
	h := m.height
	if h < 0 {
		h = 0
	}
	m.fileList.SetHeight(h)
	m.fileList.SetWidth(w)

	m.indexList.SetHeight(h)
	m.indexList.SetWidth(w)
}

func (m *Model) View() string {
	style := lipgloss.NewStyle()

	// Expanded mode - show full-screen index list
	if m.indexListExpanded {
		return lipgloss.JoinVertical(lipgloss.Left, m.indexList.View(), m.renderBottomPanel())
	}

	// Apply focus styling to borders
	leftStyle := style
	rightStyle := style

	left := leftStyle.Render(m.fileList.View())
	right := rightStyle.Render(m.indexList.View())

	lists := lipgloss.JoinHorizontal(lipgloss.Center, left, right)

	// Add bottom panel
	return lipgloss.JoinVertical(lipgloss.Left, lists, m.renderBottomPanel())
}

// renderBottomPanel renders the statistics panel with an active error line
// (reported via reportError) shown above it in red
func (m *Model) renderBottomPanel() string {
	panel := m.bottomPanel.View(m.width)

	if m.errText != "" && m.app.now().Before(m.errUntil) {
		errorLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#BB0000")).
			Background(lipgloss.Color("#1a1a1a")).
			Padding(0, 1).
			Width(m.width).
			Render(m.errText)
		return lipgloss.JoinVertical(lipgloss.Left, errorLine, panel)
	}

	return panel
}

// View renders the bottom panel
func (bp BottomPanel) View(width int) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#AAAAAA")).
		Background(lipgloss.Color("#1a1a1a")).
		Padding(0, 1)

	// Format statistics line
	statsLine := fmt.Sprintf(
		"Total: %d | Verifying: %d | Damaged: %d",
		bp.totalCount,
		bp.processingCount,
		len(bp.damagedItems),
	)

	// Degraded mode: the index database is unavailable
	if bp.storeErr != nil {
		statsLine = "DB: недоступна | " + statsLine
	}

	// Build damaged files list (truncated if too long)
	damagedLine := ""
	if len(bp.damagedItems) > 0 {
		damagedNames := make([]string, 0, len(bp.damagedItems))
		for _, d := range bp.damagedItems {
			damagedNames = append(damagedNames, filepath.Join(d.dir, d.par2Filename))
		}
		damagedLine = " | Damaged: " + strings.Join(damagedNames, ", ")
	}

	content := statsLine + damagedLine

	// Truncate to fit width
	if width > 3 && len(content) > width {
		content = content[:width-3] + "..."
	}

	return style.Width(width).Render(content)
}

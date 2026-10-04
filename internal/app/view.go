package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) Init() tea.Cmd {
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
		bottomPanel := m.bottomPanel.View(m.width)
		return lipgloss.JoinVertical(lipgloss.Left, m.indexList.View(), bottomPanel)
	}

	// Apply focus styling to borders
	leftStyle := style
	rightStyle := style

	left := leftStyle.Render(m.fileList.View())
	right := rightStyle.Render(m.indexList.View())

	lists := lipgloss.JoinHorizontal(lipgloss.Center, left, right)

	// Add bottom panel
	bottomPanel := m.bottomPanel.View(m.width)

	return lipgloss.JoinVertical(lipgloss.Left, lists, bottomPanel)
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
			damagedNames = append(damagedNames, d.dir+"/"+d.par2Filename)
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

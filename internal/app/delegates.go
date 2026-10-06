package app

import (
	"io"
	"strings"
	"unicode/utf8"

	"partui/internal/index"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileListItemDelegate struct {
	parent     *Model
	normal     lipgloss.Style
	ticked     lipgloss.Style
	archive    lipgloss.Style
	selected   lipgloss.Style
	processing lipgloss.Style
}

func newFileListItemDelegate(parent *Model) fileListItemDelegate {
	// every row starts at column 0: files and directories share one level,
	// there is no indentation
	common := lipgloss.NewStyle()
	return fileListItemDelegate{
		parent:     parent,
		normal:     common,
		ticked:     lipgloss.NewStyle().Foreground(colTicked),
		archive:    common.Foreground(colOK),
		selected:   styleCursor,
		processing: common.Foreground(colWork),
	}
}

func (d fileListItemDelegate) Height() int                               { return 1 }
func (d fileListItemDelegate) Spacing() int                              { return 0 }
func (d fileListItemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d fileListItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	var style lipgloss.Style

	it, ok := item.(fileListItem)
	if !ok {
		return
	}

	// Truncate title if it doesn't fit
	title := it.title
	titleLen := utf8.RuneCountInString(title)
	descLen := utf8.RuneCountInString(it.desc)
	availableWidth := m.Width() - descLen - 1 // -1 for space
	if availableWidth > 0 && titleLen > availableWidth {
		// Truncate by runes
		titleRunes := []rune(title)
		title = string(titleRunes[:availableWidth-1]) + "…"
	}

	spacerLen := m.Width() - utf8.RuneCountInString(title) - descLen
	if spacerLen <= 0 {
		spacerLen = 1
	}
	spacer := strings.Repeat(" ", spacerLen)
	content := title + spacer + it.desc

	if index == m.Index() && d.parent.focus == FocusFile {
		// the cursor bar is the only filled background in the UI;
		// directories keep their bold on top of it
		style := d.selected
		if it.isDir {
			style = style.Bold(true)
		}
		io.WriteString(w, style.Render(content))
		return
	}

	switch {
	case it.isDir:
		style = styleDir
	case it.selected:
		content = "* " + content
		style = d.ticked
	case it.processing:
		style = d.processing
	case it.archive:
		style = styleOK
	default:
		style = d.normal
	}

	io.WriteString(w, style.Render(content))
}

// indexListItemDelegate renders one-line index rows (plan UI_revamp,
// step UI-4): a status-colored ● marker, the file name and the dim meta
// part. The status is carried by the marker color alone — the name and the
// meta stay neutral.
type indexListItemDelegate struct {
	parent *Model
}

func newIndexListItemDelegate(parent *Model) indexListItemDelegate {
	return indexListItemDelegate{parent: parent}
}

func (d indexListItemDelegate) Height() int                               { return 1 }
func (d indexListItemDelegate) Spacing() int                              { return 0 }
func (d indexListItemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

// indexMarkerStyle returns the text style of the status marker: the status
// is carried by the marker color alone
func indexMarkerStyle(status index.Status) lipgloss.Style {
	switch status {
	case index.StatusProcessing:
		return styleWork
	case index.StatusOK:
		return styleOK
	case index.StatusDamaged:
		return styleDamaged
	default:
		return styleDim
	}
}

func (d indexListItemDelegate) Render(w io.Writer, m list.Model, idx int, item list.Item) {
	it, ok := item.(indexListItem)
	if !ok {
		return
	}

	// columns before the name: the row padding (3) plus the marker and its
	// space (2)
	const prefix = 5
	avail := max(m.Width()-prefix, 0)

	name := it.par2Filename
	meta := it.desc
	if meta != "" && utf8.RuneCountInString(name)+utf8.RuneCountInString(meta)+2 > avail {
		// keep the meta visible, give the name the remaining space; when
		// even a one-rune name does not fit, the meta is dropped
		if nameMax := avail - utf8.RuneCountInString(meta) - 2; nameMax >= 1 {
			name = truncateRunes(name, nameMax)
		} else {
			meta = ""
		}
	}

	gap := ""
	if meta != "" {
		g := max(avail-utf8.RuneCountInString(name)-utf8.RuneCountInString(meta), 2)
		gap = strings.Repeat(" ", g)
	}

	if idx == m.Index() && d.parent.focus == FocusIndex {
		// The cursor bar cannot be painted as one wrap over the whole
		// line: every inner style ends with an ANSI reset, which would
		// wipe the bar background for the rest of the line. Each piece
		// carries the bar background instead; the marker keeps its status
		// color, and Width() pads the tail with bar-colored spaces.
		marker := indexMarkerStyle(it.status).Background(colCursor).Render("●")
		body := styleCursor.Render(" " + name + gap + meta)
		io.WriteString(w, styleCursor.PaddingLeft(3).Width(m.Width()).Render(marker+body))
		return
	}

	marker := indexMarkerStyle(it.status).Render("●")
	metaPart := ""
	if meta != "" {
		metaPart = gap + styleDim.Render(meta)
	}
	io.WriteString(w, lipgloss.NewStyle().PaddingLeft(3).Render(marker+" "+name+metaPart))
}

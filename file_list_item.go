package main

import (
	"io"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileListItem struct {
	title      string
	desc       string
	isDir      bool
	size       uint64
	processing bool
	archive    bool
	selected   bool
	damaged    bool
}

func (i fileListItem) Title() string       { return i.title }
func (i fileListItem) Description() string { return i.desc }
func (i fileListItem) FilterValue() string { return i.title }

type fileListItemDelegate struct {
	parent     *Model
	normal     lipgloss.Style
	ticked     lipgloss.Style
	archive    lipgloss.Style
	selected   lipgloss.Style
	damaged    lipgloss.Style
	processing lipgloss.Style
}

func newFileListItemDelegate(parent *Model) fileListItemDelegate {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	return fileListItemDelegate{
		parent:     parent,
		normal:     common,
		ticked:     lipgloss.NewStyle().PaddingLeft(1).PaddingRight(2).Foreground(lipgloss.Color("#BBAA00")),
		archive:    common.Foreground(lipgloss.Color("#00BB00")),
		selected:   common.Background(lipgloss.Color("#0044FF")).Foreground(lipgloss.Color("#FFFFFF")),
		damaged:    common.Foreground(lipgloss.Color("#BB0000")),
		processing: common.Foreground(lipgloss.Color("#FFFF00")),
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
		title = string(titleRunes[:availableWidth-1]) + "~"
	}

	spacer_len := m.Width() - utf8.RuneCountInString(title) - descLen
	if spacer_len <= 0 {
		spacer_len = 1
	}
	spacer := strings.Repeat(" ", spacer_len)
	content := title + spacer + it.desc

	if index == m.Index() && d.parent.focus == FocusFile {
		style = d.selected
		if it.isDir {
			style = style.Foreground(lipgloss.Color("15")).Bold(true)
		}
		io.WriteString(w, style.Render(content))
		return
	}

	switch {
	case it.isDir:
		style = d.normal.Foreground(lipgloss.Color("#0044FF")).Bold(true)
	case it.selected:
		content = "* " + content
		style = d.ticked
	case it.processing:
		style = d.processing
	case it.archive:
		style = d.archive
	case it.damaged:
		style = d.damaged
	default:
		style = d.normal
	}

	io.WriteString(w, style.Render(content))
}

package main

import (
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileItem struct {
	title      string
	desc       string
	isDir      bool
	size       uint64
	processing bool
	archive    bool
	selected   bool
	damaged    bool
}

func (i fileItem) Title() string       { return i.title }
func (i fileItem) Description() string { return i.desc }
func (i fileItem) FilterValue() string { return i.title }

type itemDelegate struct {
	normal     lipgloss.Style
	ticked     lipgloss.Style
	archive    lipgloss.Style
	selected   lipgloss.Style
	damaged    lipgloss.Style
	processing lipgloss.Style
}

func newItemDelegate() itemDelegate {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	return itemDelegate{
		normal:     common,
		ticked:     lipgloss.NewStyle().PaddingLeft(1).PaddingRight(2).Foreground(lipgloss.Color("#BBAA00")),
		archive:    common.Foreground(lipgloss.Color("#00BB00")),
		selected:   common.Background(lipgloss.Color("27")).Foreground(lipgloss.Color("15")),
		damaged:    common.Foreground(lipgloss.Color("#BB0000")),
		processing: common.Foreground(lipgloss.Color("#FFFF00")),
	}
}

func (d itemDelegate) Height() int                               { return 1 }
func (d itemDelegate) Spacing() int                              { return 0 }
func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	var style lipgloss.Style

	it, ok := item.(fileItem)
	if !ok {
		return
	}

	spacer_len := m.Width() - len(it.title) - len(it.desc) - 4
	if spacer_len <= 0 {
		spacer_len = 1
	}
	spacer := strings.Repeat(" ", spacer_len)
	content := it.title + spacer + it.desc

	if index == m.Index() {
		style = d.selected
		if it.isDir {
			style = style.Foreground(lipgloss.Color("15")).Bold(true)
		}
		io.WriteString(w, style.Render(content))
		return
	}

	switch {
	case it.isDir:
		style = d.normal.Foreground(lipgloss.Color("27")).Bold(true)
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

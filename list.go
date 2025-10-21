package main

import (
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileItem struct {
	title    string
	desc     string
	isDir    bool
	archive  bool
	selected bool
}

func (i fileItem) Title() string       { return i.title }
func (i fileItem) Description() string { return i.desc }
func (i fileItem) FilterValue() string { return i.title }

type itemDelegate struct {
	normal   lipgloss.Style
	ticked   lipgloss.Style
	archive  lipgloss.Style
	selected lipgloss.Style
}

func newItemDelegate() itemDelegate {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	return itemDelegate{
		normal:   common,
		ticked:   lipgloss.NewStyle().PaddingLeft(1).PaddingRight(2).Foreground(lipgloss.Color("#BBAA00")),
		archive:  common.Foreground(lipgloss.Color("#00BB00")),
		selected: common.Background(lipgloss.Color("27")).Foreground(lipgloss.Color("15")),
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

	if it.selected {
		content = "* " + content
		style = d.ticked
		io.WriteString(w, style.Render(content))
		return
	}

	if it.archive {
		style = d.archive
	} else {
		style = d.normal
	}

	if it.isDir {
		style = style.Foreground(lipgloss.Color("27")).Bold(true)
	}
	io.WriteString(w, style.Render(content))
}

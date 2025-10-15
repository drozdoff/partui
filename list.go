package main

import (
	"fmt"
	"io"
	"slices"
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

	content := it.title + " " + it.desc

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

func buildList(m *model) list.Model {
	items := make([]list.Item, 0, len(m.entries))
	// todo: refactor string concatenation with strings.Builder
	for _, e := range m.entries {
		label := e.name

		if e.isDir {
			label = label + "/"
		}

		// cosmetic: append spaces to short filenames
		if m.filenameMaxSize > len(label) {
			label = label + strings.Repeat(" ", m.filenameMaxSize-len(label))
		}

		items = append(items, fileItem{
			title:   label,
			archive: slices.Contains(m.archived, e.name),
			desc:    fmt.Sprintf("%d bytes", e.size),
			isDir:   e.isDir,
		})
	}

	l := list.New(items, newItemDelegate(), 0, 0)
	w := m.mainWindow.width - 2
	h := m.mainWindow.height - 6
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	l.SetWidth(w)
	l.SetHeight(h)
	l.Title = m.currentDir
	l.SetShowHelp(false)
	l.SetFilterState(list.Unfiltered)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	return l
}

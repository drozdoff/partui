package main

import (
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type jobListItem struct {
	title      string
	desc       string
	processing bool
}

func (i jobListItem) Title() string       { return i.title }
func (i jobListItem) Description() string { return i.desc }
func (i jobListItem) FilterValue() string { return i.title }

type jobListItemDelegate struct {
	normal     lipgloss.Style
	processing lipgloss.Style
	done       lipgloss.Style
}

func newJobListItemDelegate() jobListItemDelegate {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	return jobListItemDelegate{
		normal:     common,
		processing: common.Foreground(lipgloss.Color("#FFFF00")),
		done:       common.Foreground(lipgloss.Color("#00BB00")),
	}
}

func (d jobListItemDelegate) Height() int                               { return 2 }
func (d jobListItemDelegate) Spacing() int                              { return 1 }
func (d jobListItemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d jobListItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	var style lipgloss.Style

	it, ok := item.(jobListItem)
	if !ok {
		return
	}

	content := it.title + "\n" + it.desc

	switch {
	case it.processing:
		style = d.processing
	case it.desc == "Done":
		style = d.done
	default:
		style = d.normal
	}

	io.WriteString(w, style.Render(content))
}

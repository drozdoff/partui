package main

import (
	"io"
	"partui/index"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type indexListItem struct {
	par2Filename string
	desc         string
	status       index.Status // processing, ok, damaged
}

func (i indexListItem) Title() string       { return i.par2Filename }
func (i indexListItem) Description() string { return i.desc }
func (i indexListItem) FilterValue() string { return i.par2Filename }

type indexListItemDelegate struct {
	parent     *Model
	selected   lipgloss.Style
	normal     lipgloss.Style
	processing lipgloss.Style
	done       lipgloss.Style
	damaged    lipgloss.Style
}

func newIndexListItemDelegate(parent *Model) indexListItemDelegate {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	return indexListItemDelegate{
		parent:     parent,
		normal:     common,
		selected:   common.Background(lipgloss.Color("#0044FF")).Foreground(lipgloss.Color("#FFFFFF")),
		processing: common.Foreground(lipgloss.Color("#FFFF00")),
		done:       common.Foreground(lipgloss.Color("#00BB00")),
		damaged:    common.Foreground(lipgloss.Color("#BB0000")),
	}
}

// GetStatusStyle returns the appropriate style for a given status
func GetStatusStyle(status index.Status) lipgloss.Style {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	switch status {
	case index.StatusProcessing:
		return common.Foreground(lipgloss.Color("#FFFF00"))
	case index.StatusOK:
		return common.Foreground(lipgloss.Color("#00BB00"))
	case index.StatusDamaged:
		return common.Foreground(lipgloss.Color("#BB0000"))
	default:
		return common
	}
}

func (d indexListItemDelegate) Height() int                               { return 2 }
func (d indexListItemDelegate) Spacing() int                              { return 1 }
func (d indexListItemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d indexListItemDelegate) Render(w io.Writer, m list.Model, idx int, item list.Item) {
	var style lipgloss.Style

	it, ok := item.(indexListItem)
	if !ok {
		return
	}

	spacer_len := m.Width() - utf8.RuneCountInString(it.par2Filename)
	if spacer_len <= 0 {
		spacer_len = 1
	}
	spacer := strings.Repeat(" ", spacer_len)

	content := it.par2Filename + spacer + "\n" + it.desc

	if idx == m.Index() && d.parent.focus == FocusIndex {
		style = d.selected
		io.WriteString(w, style.Render(content))
		return
	}

	switch it.status {
	case index.StatusProcessing:
		style = d.processing
	case index.StatusOK:
		style = d.done
	case index.StatusDamaged:
		style = d.damaged
	default:
		style = d.normal
	}

	io.WriteString(w, style.Render(content))
}

package main

import (
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// JobStatus represents the status of a PAR2 index file
type JobStatus string

const (
	JobStatusProcessing JobStatus = "processing"
	JobStatusOK         JobStatus = "ok"
	JobStatusDamaged    JobStatus = "damaged"
)

type indexListItem struct {
	par2Filename string    // имя индексного файла с расширением .par2
	desc         string    // строковое описание статуса
	status       JobStatus // processing, ok, damaged
}

func (i indexListItem) Title() string       { return i.par2Filename }
func (i indexListItem) Description() string { return i.desc }
func (i indexListItem) FilterValue() string { return i.par2Filename }

type indexListItemDelegate struct {
	normal     lipgloss.Style
	processing lipgloss.Style
	done       lipgloss.Style
	damaged    lipgloss.Style
}

func newIndexListItemDelegate() indexListItemDelegate {
	common := lipgloss.NewStyle().PaddingLeft(3).PaddingRight(2)
	return indexListItemDelegate{
		normal:     common,
		processing: common.Foreground(lipgloss.Color("#FFFF00")), // Жёлтый
		done:       common.Foreground(lipgloss.Color("#00BB00")), // Зелёный
		damaged:    common.Foreground(lipgloss.Color("#BB0000")), // Красный
	}
}

func (d indexListItemDelegate) Height() int                               { return 2 }
func (d indexListItemDelegate) Spacing() int                              { return 1 }
func (d indexListItemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d indexListItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	var style lipgloss.Style

	it, ok := item.(indexListItem)
	if !ok {
		return
	}

	content := it.par2Filename + "\n" + it.desc

	switch it.status {
	case JobStatusProcessing:
		style = d.processing
	case JobStatusOK:
		style = d.done
	case JobStatusDamaged:
		style = d.damaged
	default:
		style = d.normal
	}

	io.WriteString(w, style.Render(content))
}

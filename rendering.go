package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

const dirColor = "63"
const cursorColor = "22"

var styles = struct {
	file         lipgloss.Style
	dir          lipgloss.Style
	selectedFile lipgloss.Style
	selectedDir  lipgloss.Style
}{
	file:         lipgloss.NewStyle(),
	dir:          lipgloss.NewStyle().Foreground(lipgloss.Color(dirColor)),
	selectedFile: lipgloss.NewStyle().Background(lipgloss.Color(cursorColor)),
	selectedDir:  lipgloss.NewStyle().Foreground(lipgloss.Color(dirColor)).Background(lipgloss.Color(cursorColor)),
}

func RenderModel(m model) string {
	entries := ""
	var value string

	for i, entry := range m.entries {

		value = fmt.Sprintf("%v (%v)", entry.name, entry.size)

		switch {
		case m.cursor == i && entry.isDir:
			entries += styles.selectedDir.Render(value) + "\n"
		case m.cursor == i:
			entries += styles.selectedFile.Render(value) + "\n"
		case entry.isDir:
			entries += styles.dir.Render(value) + "\n"
		default:
			entries += styles.file.Render(value) + "\n"
		}

	}

	return entries
}

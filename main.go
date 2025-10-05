package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Entry is a file or subdirecory in a current directory
type Entry struct {
	name  string
	size  int64
	isDir bool
}

type model struct {
	currentDir string
	entries    []Entry
	cursor     int

	mainWindow struct {
		width  int
		height int
	}
}

func (m model) Init() tea.Cmd {
	return tea.SetWindowTitle("Partui")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.mainWindow.width = msg.Width
		m.mainWindow.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up":
			if m.cursor <= 0 {
				m.cursor = len(m.entries) - 1
			} else {
				m.cursor--
			}
		case "down":
			if m.cursor >= len(m.entries)-1 {
				m.cursor = 0
			} else {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	style := lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63"))

	content := fmt.Sprintf("%s\nPress Ctrl+C or q\n\n", RenderModel(m))

	return style.Width(m.mainWindow.width - 2).Height(m.mainWindow.height - 2).Render(content)
}

func buildModelForDirectory(dir string) (model, error) {
	m := model{currentDir: dir, cursor: 0}

	// unless root directory, add .. to a list of directories
	parent := filepath.Dir(dir)
	if dir != parent {
		m.entries = []Entry{{name: "..", isDir: true, size: 0}}
	}

	osFiles, err := os.ReadDir(dir)
	if err != nil {
		return m, err
	}

	for _, file := range osFiles {
		fileInfo, err := file.Info()
		if err != nil {
			return m, err
		}
		m.entries = append(m.entries, Entry{name: file.Name(), isDir: file.IsDir(), size: fileInfo.Size()})
	}

	return m, nil
}

func main() {
	name, err := filepath.Abs(os.Args[0])
	if err != nil {
		printShowStopper(err)
		os.Exit(1)
	}

	m, err := buildModelForDirectory(filepath.Dir(name))
	if err != nil {
		printShowStopper(err)
		os.Exit(1)
	}

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		printShowStopper(err)
		os.Exit(1)
	}
}

func printShowStopper(err error) {
	fmt.Printf("Something went wrong: %v", err)
}

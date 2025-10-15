package main

import (
	"fmt"
	"os"
	"os/exec"
	"partui/par2"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/list"
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
	currentDir      string
	entries         []Entry
	filenameMaxSize int
	fileList        list.Model
	archived        []string

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
		// size list to fit inside the bordered box from View()
		w := m.mainWindow.width - 2
		h := m.mainWindow.height - 6
		if w < 0 {
			w = 0
		}
		if h < 0 {
			h = 0
		}
		m.fileList.SetWidth(w)
		m.fileList.SetHeight(h)
		var cmd tea.Cmd
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case " ":
			item := m.fileList.SelectedItem().(fileItem)
			if !item.isDir && !item.archive {
				item.selected = !item.selected
				m.fileList.SetItem(m.fileList.Index(), item)
				m.fileList.CursorDown()
			}
		case "c":
			item := m.fileList.SelectedItem().(fileItem)
			createParFiles(m, item)
			item.archive = true
			m.fileList.SetItem(m.fileList.Index(), item)
			m.fileList.CursorDown()
		case "enter":
			item := m.fileList.SelectedItem().(fileItem)
			if item.isDir {
				var err error
				dir := m.currentDir + "/" + item.title
				err = buildModelForDirectory(&m, filepath.Dir(dir))
				printShowStopper(err)
			}
		}
		var cmd tea.Cmd
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	}
	return m, nil
}

func createParFiles(m model, item fileItem) {
	name := strings.TrimRight(item.title, " \t\r\n")
	cmd := exec.Command("par2", "c", name)
	cmd.Dir = m.currentDir
	_, err := cmd.CombinedOutput()
	// todo: check output and handle errors
	printShowStopper(err)
}

func (m model) View() string {
	style := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("63"))

	content := fmt.Sprintf("%s\nPress Ctrl+C or q\n\n", m.fileList.View())

	return style.Width(m.mainWindow.width - 2).Height(m.mainWindow.height - 2).Render(content)
}

func buildModelForDirectory(m *model, dir string) error {
	m.currentDir = dir

	// unless root directory, add .. to a list of directories
	parent := filepath.Dir(dir)
	if dir != parent {
		m.entries = []Entry{{name: "..", isDir: true, size: 0}}
	}

	osFiles, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, file := range osFiles {
		fileInfo, err := file.Info()
		if err != nil {
			return err
		}

		if isParIndex(file.Name()) {
			archived, err := par2.ListPar2Filenames(m.currentDir + "/" + file.Name())
			if err != nil {
				// todo: non show stopper error
				return err
			}
			m.archived = append(m.archived, archived...)
		}

		if m.filenameMaxSize < len(file.Name()) {
			m.filenameMaxSize = len(file.Name())
		}

		m.entries = append(m.entries, Entry{name: file.Name(), isDir: file.IsDir(), size: fileInfo.Size()})
	}

	m.fileList = buildList(m)

	return nil
}

func isParIndex(filename string) bool {
	// PAR2 index files end with ".par2" and do not include a volume suffix like ".vol000+01" before the extension
	lf := strings.ToLower(filename)
	if !strings.HasSuffix(lf, ".par2") {
		return false
	}
	// Detect volume files: name.vol<digits>+<digits>.par2
	var volPattern = regexp.MustCompile(`\.vol\d+\+\d+\.par2$`)
	return !volPattern.MatchString(lf)
}

func main() {
	name, err := filepath.Abs(os.Args[0])
	printShowStopper(err)

	m := model{}
	err = buildModelForDirectory(&m, filepath.Dir(name))
	printShowStopper(err)

	p := tea.NewProgram(m, tea.WithAltScreen())

	_, err = p.Run()
	printShowStopper(err)
}

func printShowStopper(err error) {
	if err != nil {
		fmt.Printf("Something went wrong: %v", err)
		os.Exit(1)
	}
}

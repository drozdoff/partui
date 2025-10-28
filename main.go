package main

import (
	"fmt"
	"os"
	"os/exec"
	"partui/par2"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	currentDir string
	fileList   list.Model
	archived   []string

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
		if w < 0 {
			w = 0
		}
		m.fileList.SetWidth(w)

		h := m.mainWindow.height - 6
		if h < 0 {
			h = 0
		}
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
		case "v":
			item := m.fileList.SelectedItem().(fileItem)
			verifyParFiles(m, item)
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
	cmd := exec.Command("par2", "c", item.title)
	cmd.Dir = m.currentDir
	_, err := cmd.CombinedOutput()
	// todo: check output and handle errors
	printShowStopper(err)
}

func verifyParFiles(m model, item fileItem) {
	cmd := exec.Command("par2", "v", item.title)
	cmd.Dir = m.currentDir
	output, err := cmd.CombinedOutput()
	if err != nil && err.Error() == "exit status 1" {
		// parse and show per-file repair necessity
		results := parsePar2VerifyLog(string(output))
		for _, needs := range results {
			if needs {
				item.damaged = true
				m.fileList.SetItem(m.fileList.Index(), item)
			}
		}
	}
}

func (m model) View() string {
	style := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("63"))

	content := fmt.Sprintf("%s\nPress Ctrl+C or q\n\n", m.fileList.View())

	return style.Width(m.mainWindow.width - 2).Height(m.mainWindow.height - 2).Render(content)
}

func buildModelForDirectory(m *model, dir string) error {
	m.currentDir = dir

	osFiles, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	items := make([]list.Item, 0, len(osFiles)+1)

	// unless root directory, add .. to a list of directories
	parent := filepath.Dir(dir)
	if dir != parent {
		items = append(items, fileItem{title: "../", isDir: true, desc: "Parent directory"})
	}

	// populate information about already archived files first
	processIndexFiles(m, osFiles)

	// build a list of files and directories to display
	for _, file := range osFiles {
		name := file.Name()

		fileInfo, err := file.Info()
		if err != nil || hiddenFile(name) {
			continue
		}

		title := name
		if file.IsDir() {
			title = title + "/"
		}

		items = append(items, fileItem{
			title:   title,
			archive: slices.Contains(m.archived, name),
			desc:    fmt.Sprintf("%d bytes", fileInfo.Size()),
			isDir:   file.IsDir(),
		})
	}

	m.fileList = list.New(items, newItemDelegate(), 0, 0)
	w := m.mainWindow.width - 2
	if w < 0 {
		w = 0
	}
	h := m.mainWindow.height - 6
	if h < 0 {
		h = 0
	}
	m.fileList.SetWidth(w)
	m.fileList.SetHeight(h)
	m.fileList.Title = m.currentDir
	m.fileList.SetShowHelp(false)
	m.fileList.SetFilterState(list.Unfiltered)
	m.fileList.SetShowPagination(true)
	m.fileList.DisableQuitKeybindings()

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

func hiddenFile(name string) bool {
	// ignore hidden files and .par2 files
	return strings.HasPrefix(name, ".") || filepath.Ext(name) == ".par2"
}

func processIndexFiles(m *model, files []os.DirEntry) {
	for _, file := range files {
		name := file.Name()
		if isParIndex(name) {
			archived, err := par2.ListPar2Filenames(m.currentDir + "/" + name)
			if err != nil {
				// todo: error handling
				continue
			}
			m.archived = append(m.archived, archived...)
		}
	}
}

// parsePar2VerifyLog parses the output of `par2 v` and returns a map of
// filename to a boolean indicating whether repair is needed for that file.
// It recognizes lines in the form:
//
//	Target: "<name>" - ok.
//	Target: "<name>" - damaged. Found X of Y data blocks.
//
// Files marked as "damaged", "missing" or "corrupt"
// will be treated as needing repair.
func parsePar2VerifyLog(log string) map[string]bool {
	results := make(map[string]bool)

	// Match: Target: "<file>" - <status>
	// Example: Target: "main.go" - damaged. Found 1270 of 1307 data blocks.
	targetRe := regexp.MustCompile(`(?m)^.*Target:\s+"(.+)"\s+-\s+(.+).*$`)

	matches := targetRe.FindAllStringSubmatch(log, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		name := m[1]
		status := strings.ToLower(m[2])

		needsRepair := false
		switch {
		case strings.Contains(status, "ok"):
			needsRepair = false
		case strings.Contains(status, "damaged"):
			needsRepair = true
		case strings.Contains(status, "missing"):
			needsRepair = true
		case strings.Contains(status, "corrupt"):
			needsRepair = true
		}

		results[name] = needsRepair
	}

	return results
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

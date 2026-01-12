package main

import (
	"fmt"
	"os"
	"partui/par2"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	gopar "github.com/akalin/gopar/par2"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	currentDir string
	fileList   list.Model
	archived   []string

	mainWindow struct {
		width  int
		height int
	}
}

var numProcs = runtime.GOMAXPROCS(-1)

func (m Model) Init() tea.Cmd {
	return tea.SetWindowTitle("Partui")
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			createParFiles(&m, item)
			item.archive = true
			m.fileList.SetItem(m.fileList.Index(), item)
			m.fileList.CursorDown()
		case "v":
			item := m.fileList.SelectedItem().(fileItem)
			verifyParFiles(&m, item)
		// TODO: repair damaged files
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

func createParFiles(m *Model, item fileItem) {
	// TODO: create par files for selected files or for a file under cursor in no selected files
	// one index per selected file is default.
	// override default behaviour in config??? specify index file name?
	// UI for config?
	// TODO: concurrent par files creation in case of a list
	parPath := m.currentDir + "/" + item.title + ".par2"
	filePaths := []string{m.currentDir + "/" + item.title}
	options := gopar.CreateOptions{
		SliceByteCount:  -1, // TODO: calculate block size from a file size
		NumParityShards: -1,
		NumGoroutines:   numProcs,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}
	err := gopar.Create(parPath, filePaths, options)
	printShowStopper(err)
}

func verifyParFiles(m *Model, item fileItem) {
	// TODO: propper logging
	// TODO: how to manage existing par2 index files with different names?
	parPath := m.currentDir + "/" + item.title + ".par2"
	options := gopar.VerifyOptions{
		NumGoroutines:  numProcs,
		VerifyDelegate: &par2.FilenameCollector{},
	}
	res, err := gopar.Verify(parPath, options)
	if err != nil {
		m.fileList.NewStatusMessage(item.title + " can not be verified")
		return
	}

	if res.ShardCounts.RepairNeeded() {
		item.damaged = true
		m.fileList.SetItem(m.fileList.Index(), item)
		m.fileList.NewStatusMessage(item.title + " damaged")
		return
	}

	m.fileList.NewStatusMessage(item.title + " is OK")
}

func (m Model) View() string {
	style := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("63"))

	content := fmt.Sprintf("%s\nPress Ctrl+C or q\n\n", m.fileList.View())

	return style.Width(m.mainWindow.width - 2).Height(m.mainWindow.height - 2).Render(content)
}

func buildModelForDirectory(m *Model, dir string) error {
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

func processIndexFiles(m *Model, files []os.DirEntry) {
	for _, file := range files {
		name := file.Name()
		if isParIndex(name) {
			archived, err := par2.ListPar2Filenames(m.currentDir + "/" + name)
			if err != nil {
				// TODO: error handling
				continue
			}
			m.archived = append(m.archived, archived...)
		}
	}
}

func main() {
	name, err := filepath.Abs(os.Args[0])
	printShowStopper(err)

	m := Model{}
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

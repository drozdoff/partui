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
	"sync"

	gopar "github.com/akalin/gopar/par2"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	currentDir    string
	filesSelected int
	fileList      list.Model
	archived      []string

	mainWindow struct {
		width  int
		height int
	}
}

type processingFinishedMsg struct {
	dir   string
	title string
}

type processingErrorMsg struct {
	dir   string
	title string
	err   error
}

// TODO: make percentage configurable
const percentage = 5

var numProcs = runtime.GOMAXPROCS(-1)

// counting semaphore for any kind of workers
var workers = make(chan struct{}, numProcs)

var wg sync.WaitGroup

func (m Model) Init() tea.Cmd {
	return tea.SetWindowTitle("Partui")
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

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

		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case " ":
			item := m.fileList.SelectedItem().(fileItem)
			if !item.isDir && !item.archive {
				if item.selected {
					m.filesSelected -= 1
				} else {
					m.filesSelected += 1
				}
				item.selected = !item.selected
				m.fileList.SetItem(m.fileList.Index(), item)
				m.fileList.CursorDown()
			}
		case "c":
			// TODO: create par files for selected files or for a file under cursor in no selected files
			// one index per selected file is default.
			// override default behaviour in config??? specify index file name?
			// UI for config?
			// TODO: concurrent par files creation in case of a list
			// TODO: validation for file uniqueness
			if m.filesSelected > 0 {
				//
			} else {
				item := m.fileList.SelectedItem().(fileItem)
				item.processing = true
				m.fileList.SetItem(m.fileList.Index(), item)
				m.fileList.CursorDown()
				wg.Add(1)
				go createParFiles(m.currentDir, item.title, item.size)
			}
			return m, m.fileList.StartSpinner()
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

		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	case processingFinishedMsg:
		title := msg.title
		index, item := findFileItem(m.fileList.Items(), title)
		if index != -1 {
			item.processing = false
			item.archive = true
			m.fileList.SetItem(index, item)
			m.fileList, cmd = m.fileList.Update(msg)
			m.fileList.StopSpinner()
			return m, cmd
		}
	case processingErrorMsg:
		// TODO: Log processing error
		title := msg.title
		index, item := findFileItem(m.fileList.Items(), title)
		if index != -1 {
			item.processing = false
			m.fileList.SetItem(index, item)
			m.fileList, cmd = m.fileList.Update(msg)
			return m, cmd
		}
	case spinner.TickMsg:
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	}

	return m, nil
}

func findFileItem(items []list.Item, title string) (int, fileItem) {
	for i, item := range items {
		fi := item.(fileItem)
		if fi.title == title {
			return i, fi
		}
	}
	// no item found is possible scenario if dir is changed while some files are still processing
	return -1, fileItem{}
}

func createParFiles(dir string, title string, size uint64) {
	workers <- struct{}{}
	defer func() {
		<-workers
		wg.Done()
	}()

	filePath := dir + "/" + title
	parPath := filePath + ".par2"

	options := gopar.CreateOptions{
		SliceByteCount:  par2.CalculateBlockSize(size, percentage),
		NumParityShards: -1,
		NumGoroutines:   numProcs,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}

	err := gopar.Create(parPath, []string{filePath}, options)
	if err != nil {
		program.Send(processingErrorMsg{dir: dir, title: title, err: err})
		return
	}

	program.Send(processingFinishedMsg{dir: dir, title: title})
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
		m.fileList.NewStatusMessage(item.title + "is damaged")
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
			size:    uint64(fileInfo.Size()),
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

var program *tea.Program

func main() {
	name, err := filepath.Abs(os.Args[0])
	printShowStopper(err)

	m := Model{}
	err = buildModelForDirectory(&m, filepath.Dir(name))
	printShowStopper(err)

	program = tea.NewProgram(m, tea.WithAltScreen())

	_, err = program.Run()
	printShowStopper(err)
	wg.Wait()
}

func printShowStopper(err error) {
	if err != nil {
		fmt.Printf("Something went wrong: %v", err)
		os.Exit(1)
	}
}

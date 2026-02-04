package main

import (
	"context"
	"fmt"
	"os"
	"partui/par2"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	gopar "github.com/akalin/gopar/par2"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Job struct {
	jobType     string
	name        string
	description string
}

type Model struct {
	fileList list.Model
	jobList  list.Model

	currentDir    string
	filesSelected int
	archived      []string

	width  int
	height int
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
var program *tea.Program
var ctx context.Context

func (m *Model) Init() tea.Cmd {
	return tea.SetWindowTitle("Partui")
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// size list to fit inside the bordered box from View()

		m.jobList.Update(msg)
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case " ":
			item := m.fileList.SelectedItem().(fileListItem)
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
			// TODO: Now one index file per selected file is default.
			// override this behaviour in config??? specify index file name for a list of files?
			// UI for config?
			// TODO: handle change dir during process. file list is now strictly for current dir.
			if m.filesSelected > 0 {
				for index, i := range m.fileList.Items() {
					item := i.(fileListItem)
					if item.selected {
						item.processing = true
						item.selected = false
						m.fileList.SetItem(index, item)
						m.addJob(item.title)
						wg.Add(1)
						go createParFiles(m.currentDir, item.title, item.size)
					}
				}
			} else {
				item := m.fileList.SelectedItem().(fileListItem)
				item.processing = true
				m.fileList.SetItem(m.fileList.Index(), item)
				m.fileList.CursorDown()
				m.addJob(item.title)
				wg.Add(1)
				go createParFiles(m.currentDir, item.title, item.size)
			}
			return m, m.fileList.StartSpinner()
		case "v":
			item := m.fileList.SelectedItem().(fileListItem)
			verifyParFiles(m, item)
		// TODO: repair damaged files
		case "enter":
			item := m.fileList.SelectedItem().(fileListItem)
			if item.isDir {
				var err error
				dir := m.currentDir + "/" + item.title
				newDir := filepath.Dir(dir)

				// Save current dir to select when going back
				var prevDir string = ""
				if item.title == "../" {
					prevDir = m.currentDir
				}

				err = buildModelForDirectory(m, newDir)
				printShowStopper(err)
				// If going to parent, select the directory we came from
				if prevDir != "" {
					idx := findDirIndex(m.fileList.Items(), prevDir)
					if idx > 0 {
						m.fileList.Select(idx)
					}
				}
			}
		}

		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	case processingFinishedMsg:
		title := msg.title
		index, item := findFileListItem(m.fileList.Items(), title)
		if index != -1 {
			item.processing = false
			item.archive = true
			m.fileList.SetItem(index, item)
			m.fileList, cmd = m.fileList.Update(msg)
			m.fileList.StopSpinner()
		}

		// Update jobList - find the job and mark it as completed
		jobItems := m.jobList.Items()
		jiIndex, _ := findJobListItem(jobItems, title)
		if jiIndex != -1 {
			ji := jobItems[jiIndex].(jobListItem)
			ji.processing = false
			ji.desc = "Done"
			jobItems[jiIndex] = ji
			m.jobList.SetItems(jobItems)
		}

		return m, cmd
	case processingErrorMsg:
		// TODO: Log processing error
		title := msg.title
		index, item := findFileListItem(m.fileList.Items(), title)
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

func findFileListItem(items []list.Item, title string) (int, fileListItem) {
	for i, item := range items {
		fi := item.(fileListItem)
		if fi.title == title {
			return i, fi
		}
	}
	// no item found is possible scenario if dir is changed while some files are still processing
	return -1, fileListItem{}
}

func findJobListItem(items []list.Item, title string) (int, jobListItem) {
	for i, item := range items {
		ji := item.(jobListItem)
		if ji.title == title {
			return i, ji
		}
	}
	return -1, jobListItem{}
}

func (m *Model) addJob(title string) {
	jobItems := m.jobList.Items()
	jobItems = append(jobItems, jobListItem{
		title:      title,
		desc:       "Processing...",
		processing: true,
	})
	m.jobList.SetItems(jobItems)
}

func createParFiles(dir string, title string, size uint64) {
	workers <- struct{}{}
	defer func() {
		<-workers
		wg.Done()
	}()

	// don't start a new file if program exited
	select {
	case <-ctx.Done():
		return
	default:
		// do nothing
	}

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

func verifyParFiles(m *Model, item fileListItem) {
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

func (m *Model) View() string {
	style := lipgloss.NewStyle()

	w := m.width / 2
	h := m.height - 6
	if h < 0 {
		h = 0
	}
	m.fileList.SetHeight(h)
	m.fileList.SetWidth(w)

	m.jobList.SetHeight(h)
	m.jobList.SetWidth(w)

	left := style.Render(fmt.Sprintf("%s\nPress Ctrl+C or q\n", m.fileList.View()))
	rigth := style.Render(fmt.Sprintf("%s\nEnd of job list\n", m.jobList.View()))

	return lipgloss.JoinHorizontal(lipgloss.Center, left, rigth)
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
		items = append(items, fileListItem{title: "../", isDir: true, desc: "Parent directory"})
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

		items = append(items, fileListItem{
			title:   title,
			size:    uint64(fileInfo.Size()),
			archive: slices.Contains(m.archived, name),
			desc:    fmt.Sprintf("%d bytes", fileInfo.Size()),
			isDir:   file.IsDir(),
		})
	}

	// Sorting: directories first, then files, by name
	sort.Slice(items, func(i, j int) bool {
		a := items[i].(fileListItem)
		b := items[j].(fileListItem)
		// If types are different - directories first
		if a.isDir != b.isDir {
			return a.isDir
		}
		return a.title < b.title
	})

	m.fileList = list.New(items, newFileListItemDelegate(), 0, 0)
	m.fileList.Title = m.currentDir
	m.fileList.SetShowHelp(false)
	m.fileList.SetShowStatusBar(false)
	m.fileList.SetFilterState(list.Unfiltered)
	m.fileList.SetShowPagination(true)
	m.fileList.DisableQuitKeybindings()

	return nil
}

func findDirIndex(items []list.Item, dirName string) int {
	// dirName is full path, we need to find the directory entry
	// The entry title will be "dirname/"
	targetTitle := filepath.Base(dirName) + "/"
	for i, item := range items {
		fi := item.(fileListItem)
		if fi.isDir && fi.title == targetTitle {
			return i
		}
	}
	return 0
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

	jobItems := make([]list.Item, 0)
	m.jobList = list.New(jobItems, newJobListItemDelegate(), 0, 0)
	m.jobList.Title = "Active Jobs"
	m.jobList.SetShowHelp(false)
	m.jobList.SetShowStatusBar(false)
	m.jobList.SetFilterState(list.Unfiltered)
	m.jobList.SetShowPagination(false)
	m.jobList.DisableQuitKeybindings()

	err = buildModelForDirectory(&m, filepath.Dir(name))
	printShowStopper(err)

	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	program = tea.NewProgram(&m, tea.WithAltScreen(), tea.WithContext(ctx))

	_, err = program.Run()
	cancel()
	wg.Wait()
	printShowStopper(err)
}

func printShowStopper(err error) {
	if err != nil {
		fmt.Printf("Something went wrong: %v", err)
		os.Exit(1)
	}
}

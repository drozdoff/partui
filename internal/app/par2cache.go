package app

// Session cache of parsed .par2 contents and the background load behind it
// (plan item 7.2). Parsing a .par2 header is file IO, so it must not run
// inside navigation: the model build marks the not-yet-parsed archives and
// loadArchivedCmd parses them off the UI goroutine. The cache keeps the
// parsed data file names for the lifetime of the process and is invalidated
// when the file on disk changes (size or modification time) or when the app
// itself rewrites an archive.

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"partui/internal/index"
	"partui/internal/par2"

	tea "github.com/charmbracelet/bubbletea"
)

// par2NamesEntry is one cached parse result. The error is kept as text: it
// only feeds the item description, the full reason went to the log at parse
// time. An entry with err != "" means the file exists but cannot be parsed.
type par2NamesEntry struct {
	names   []string
	err     string
	modTime time.Time
	size    int64
}

// par2NameCache maps dir → par2 file name → entry. All methods are safe for
// concurrent use: lookups come from the UI goroutine, parse results are
// published from background tea.Cmd goroutines.
type par2NameCache struct {
	mu      sync.Mutex
	entries map[string]map[string]par2NamesEntry
}

func newPar2NameCache() *par2NameCache {
	return &par2NameCache{entries: make(map[string]map[string]par2NamesEntry)}
}

// get returns the cached parse result if the file is unchanged since it was
// cached. A missing, unreadable or changed file is a miss; get never parses.
func (c *par2NameCache) get(dir, name string) (par2NamesEntry, bool) {
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return par2NamesEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[dir][name]
	if !ok || !e.modTime.Equal(info.ModTime()) || e.size != info.Size() {
		return par2NamesEntry{}, false
	}
	return e, true
}

// put parses the archive and caches the result. A file whose metadata cannot
// be read is not cached — the entry could never validate anyway.
func (c *par2NameCache) put(dir, name string) par2NamesEntry {
	path := filepath.Join(dir, name)
	names, err := par2.ListPar2Filenames(path)

	e := par2NamesEntry{names: names}
	if err != nil {
		e.err = err.Error()
	}
	if info, statErr := os.Stat(path); statErr == nil {
		e.modTime, e.size = info.ModTime(), info.Size()
		c.mu.Lock()
		if c.entries[dir] == nil {
			c.entries[dir] = make(map[string]par2NamesEntry)
		}
		c.entries[dir][name] = e
		c.mu.Unlock()
	}
	return e
}

// seed records a parse result without parsing. The caller guarantees the
// names match the file it just wrote; used after PAR2 creation, because on
// filesystems with coarse modification times the new file could otherwise
// compare equal to a stale entry.
func (c *par2NameCache) seed(dir, name string, names []string) {
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[dir] == nil {
		c.entries[dir] = make(map[string]par2NamesEntry)
	}
	c.entries[dir][name] = par2NamesEntry{names: slices.Clone(names), modTime: info.ModTime(), size: info.Size()}
}

// loadArchivedCmd parses the given .par2 index files of dir in the
// background and reports the results — data file names or the parse error —
// as one archivedLoadedMsg. Fresh cache entries are used as-is, so
// revisiting a directory does no par2 IO at all. The command is deliberately
// not tracked by app.wg: it only reads files, fills the cache and creates at
// most one store record per file; a result arriving after the user navigated
// away is dropped by the model, and one arriving after shutdown is never
// delivered — the program's event loop is already gone.
func (m *Model) loadArchivedCmd(dir string, names []string) tea.Cmd {
	app := m.app
	return func() tea.Msg {
		results := make([]par2Content, 0, len(names))
		for _, name := range names {
			entry, cached := app.par2Names.get(dir, name)
			if !cached {
				entry = app.par2Names.put(dir, name)
				if entry.err != "" {
					app.logger.Warn("failed to list PAR2 filenames",
						slog.String("dir", dir),
						slog.String("filename", name),
						slog.String("error", entry.err))
				}
			}

			res := par2Content{par2Filename: name}
			if entry.err != "" {
				res.err = errors.New(entry.err)
			} else {
				res.names = entry.names
				res.storedAt = app.ensureIndexRecord(dir, name)
			}
			results = append(results, res)
		}
		return archivedLoadedMsg{dir: dir, results: results}
	}
}

// ensureIndexRecord creates the initial OK record for a newly discovered
// index. The write is deferred here from the model build — which no longer
// records archives it has not parsed — so unparseable files never enter the
// database. It reports the record time, or zero when the record already
// existed or could not be written.
func (a *App) ensureIndexRecord(dir, name string) time.Time {
	descriptor, err := a.store.Get(dir, name)
	switch {
	case err != nil:
		a.logStoreError("failed to check index in database", err)
	case descriptor == nil:
		now := a.now()
		if err := a.store.Put(dir, name, index.StatusOK, now); err != nil {
			a.logStoreError("failed to save index to database", err)
			return time.Time{}
		}
		return now
	}
	return time.Time{}
}

// handleArchivedLoaded applies the background parse results: broken archives
// switch from the optimistic default to the honest Unverifiable status,
// freshly stored records light up their LastVerified, and the file list gets
// its archive marks. Results of a directory the user has already left are
// dropped.
func (m *Model) handleArchivedLoaded(msg archivedLoadedMsg) tea.Cmd {
	if msg.dir != m.currentDir {
		return nil
	}

	for _, res := range msg.results {
		if res.err == nil {
			m.archived = append(m.archived, res.names...)
			// In the expanded mode the index list holds database records of
			// all directories, possibly several with the same file name —
			// index item updates are skipped there; the corrected values
			// appear when the list is collapsed back and rebuilt
			if !m.indexListExpanded && !res.storedAt.IsZero() {
				m.updateIndexItem(res.par2Filename, func(ii *indexListItem) {
					ii.lastVerified = res.storedAt
				})
			}
			continue
		}

		if m.indexListExpanded {
			continue
		}

		// the full reason went to the log at parse time; the item shows the
		// compressed one. updateIndexItem is not used because it would
		// overwrite the custom description with the standard rendering
		idx, item := findListItem(m.indexList.Items(), res.par2Filename, func(it indexListItem) string { return it.par2Filename })
		if idx >= 0 {
			item.status = index.StatusUnverifiable
			item.desc = "не удалось прочитать: " + shortReason(res.err.Error())
			m.indexList.SetItem(idx, item)
		}
	}

	// the archive marks of the file list are additive: the build already set
	// the marks of the cached archives, this adds the freshly parsed ones
	for i, it := range m.fileList.Items() {
		fi, ok := it.(fileListItem)
		if !ok || fi.isDir || fi.archive {
			continue
		}
		if slices.Contains(m.archived, fi.title) {
			fi.archive = true
			m.fileList.SetItem(i, fi)
		}
	}

	return nil
}

package app

import (
	"time"

	"partui/internal/index"
)

// Custom messages to be sent to main process

type parCreateFinishedMsg struct {
	dir   string
	title string
	size  uint64
}

type parCreateErrorMsg struct {
	dir   string
	title string
	err   error
}

type indexVerifyFinishedMsg struct {
	dir          string
	par2Filename string
	status       index.Status
	err          error
}

// par2Content is the parse result for one .par2 index file, as reported to
// the UI by the background load (plan item 7.2)
type par2Content struct {
	par2Filename string
	names        []string // data files inside the archive; empty on err
	err          error
	// storedAt is set when the loader created the initial OK record for an
	// index that was not yet present in the database
	storedAt time.Time
}

// archivedLoadedMsg reports the parsed contents of the .par2 index files of
// one directory
type archivedLoadedMsg struct {
	dir     string
	results []par2Content
}

// clearErrorMsg removes the error line shown by Model.reportError after
// its display time is up
type clearErrorMsg struct{}

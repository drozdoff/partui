package app

import "partui/internal/index"

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

// clearErrorMsg removes the error line shown by Model.reportError after
// its display time is up
type clearErrorMsg struct{}

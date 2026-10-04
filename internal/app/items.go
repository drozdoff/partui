package app

import (
	"partui/internal/index"
	"time"
)

// fileListItem is an entry of the file list
type fileListItem struct {
	title      string
	desc       string
	isDir      bool
	size       uint64
	processing bool
	archive    bool
	selected   bool
}

func (i fileListItem) Title() string       { return i.title }
func (i fileListItem) Description() string { return i.desc }
func (i fileListItem) FilterValue() string { return i.title }

// indexListItem is an entry of the index list
type indexListItem struct {
	par2Filename string
	dir          string
	desc         string
	status       index.Status // processing, ok, damaged
	lastVerified time.Time
}

func (i indexListItem) Title() string       { return i.par2Filename }
func (i indexListItem) Description() string { return i.desc }
func (i indexListItem) FilterValue() string { return i.par2Filename }

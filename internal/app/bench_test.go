package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"partui/internal/index"
	"partui/internal/par2"

	gopar "github.com/akalin/gopar/par2"
)

// Benchmarks for plan item 7.2: the cost of entering a directory with
// 50 .par2 index files. Cold is the first visit — nothing parsed yet; Warm
// is revisiting an already parsed directory. The background load command is
// deliberately not run here: the benchmark measures how much the model
// build itself blocks navigation.

const (
	benchFileCount = 50
	benchFileSize  = 64_000
)

// benchDirectory creates a directory with benchFileCount data files, each
// with a real PAR2 archive next to it
func benchDirectory(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	content := bytes.Repeat([]byte("x"), benchFileSize)
	for i := range benchFileCount {
		title := fmt.Sprintf("file%02d.txt", i)
		data := filepath.Join(dir, title)
		if err := os.WriteFile(data, content, 0o644); err != nil {
			b.Fatal(err)
		}
		runner := goparRunner{}
		if err := runner.Create(data+".par2", []string{data}, gopar.CreateOptions{
			SliceByteCount:  par2.CalculateBlockSize(benchFileSize, 5),
			NumParityShards: -1,
			NumGoroutines:   1,
			CreateDelegate:  gopar.DoNothingCreateDelegate{},
		}); err != nil {
			b.Fatal(err)
		}
	}
	return dir
}

// benchStore opens a real bbolt store and pre-creates an OK record for
// every index, so the timed loop measures reads only
func benchStore(b *testing.B, dir string) IndexStore {
	b.Helper()
	app := newTestApp()
	st, err := newBboltStore(filepath.Join(b.TempDir(), "indexes.db"), app.ctx, app.logger)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })

	fixed := time.Unix(1700000000, 0)
	for i := range benchFileCount {
		if err := st.Put(dir, fmt.Sprintf("file%02d.txt.par2", i), index.StatusOK, fixed); err != nil {
			b.Fatal(err)
		}
	}
	return st
}

func BenchmarkBuildModelForDirectory_Cold(b *testing.B) {
	dir := benchDirectory(b)
	st := benchStore(b, dir)

	for b.Loop() {
		// a fresh model per iteration — nothing parsed yet
		m := newTestModel()
		m.app.store = st
		if _, err := m.buildModelForDirectory(dir); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildModelForDirectory_Warm(b *testing.B) {
	dir := benchDirectory(b)
	st := benchStore(b, dir)

	m := newTestModel()
	m.app.store = st
	cmd, err := m.buildModelForDirectory(dir)
	if err != nil {
		b.Fatal(err)
	}
	if cmd != nil {
		// the first load fills the session cache the way the running
		// program would; without it every iteration would re-parse
		cmd()
	}

	for b.Loop() {
		if _, err := m.buildModelForDirectory(dir); err != nil {
			b.Fatal(err)
		}
	}
}

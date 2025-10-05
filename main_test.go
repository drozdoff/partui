package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func prepareDirecrory(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()

	fileNames := []string{"a.txt", "b.bin"}
	for _, name := range fileNames {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("content"), 0o644); err != nil {
			t.Fatalf("failed to create temp file %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(tempDir, "cdir"), 0o755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	return tempDir
}

func TestBuildModelForDirectory(t *testing.T) {
	dir := prepareDirecrory(t)
	m, err := buildModelForDirectory(dir)

	assert.Nil(t, err)

	assert.Equal(t, dir, m.currentDir)

	// Expect 4 entries
	assert.Equal(t, 4, len(m.entries))
	expected := []string{"..", "a.txt", "b.bin", "cdir"}
	for i, f := range m.entries {
		assert.Equal(t, expected[i], f.name)
	}

	_, err = buildModelForDirectory(filepath.Join(os.TempDir(), "non-existent-dir"))
	assert.Error(t, err)
}

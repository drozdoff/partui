package par2

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gopar "github.com/akalin/gopar/par2"
)

// Benchmarks of the streaming verify against its gopar counterpart on the
// same 100 MB corpus (plans/streaming_file.md §5.6). The speed is a side
// goal; the acceptance criterion is the RSS bound pinned by
// TestVerifyStreamRSS.

func benchCorpus(b *testing.B, size int) string {
	b.Helper()
	dir := b.TempDir()
	data := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(data, randomBytes(size, 42), 0o644); err != nil {
		b.Fatal(err)
	}
	parPath := data + ".par2"
	if err := gopar.Create(parPath, []string{data}, gopar.CreateOptions{
		SliceByteCount:  CalculateBlockSize(uint64(size), 5),
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}); err != nil {
		b.Fatal(err)
	}
	return parPath
}

func BenchmarkVerifyStream(b *testing.B) {
	parPath := benchCorpus(b, 100<<20)
	for b.Loop() {
		if _, err := VerifyStream(context.Background(), parPath, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGoparVerify(b *testing.B) {
	parPath := benchCorpus(b, 100<<20)
	for b.Loop() {
		if _, err := gopar.Verify(parPath, gopar.VerifyOptions{NumGoroutines: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

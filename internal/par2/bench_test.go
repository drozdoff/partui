package par2

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gopar "github.com/akalin/gopar/par2"
)

// Benchmarks of the streaming verify and create against their gopar
// counterparts on the same 100 MB corpus (plans/streaming_file.md §5.6,
// §6.7). The speed is a side goal; the acceptance criteria are the RSS
// bounds pinned by TestVerifyStreamRSS and TestCreateStreamRSS.

func benchData(b *testing.B, size int) string {
	b.Helper()
	data := filepath.Join(b.TempDir(), "data.bin")
	if err := os.WriteFile(data, randomBytes(size, 42), 0o644); err != nil {
		b.Fatal(err)
	}
	return data
}

func benchCorpus(b *testing.B, size int) string {
	b.Helper()
	data := benchData(b, size)
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

// The create benchmarks run both implementations with the app's option
// shape: the CalculateBlockSize slice and GOMAXPROCS goroutines.
func BenchmarkCreateStream(b *testing.B) {
	data := benchData(b, 100<<20)
	opts := CreateOptions{
		SliceByteCount:  CalculateBlockSize(100<<20, 5),
		NumParityShards: -1,
		NumGoroutines:   runtime.GOMAXPROCS(0),
	}
	for b.Loop() {
		if err := CreateStream(context.Background(), data+".par2", []string{data}, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGoparCreate(b *testing.B) {
	data := benchData(b, 100<<20)
	opts := gopar.CreateOptions{
		SliceByteCount:  CalculateBlockSize(100<<20, 5),
		NumParityShards: -1,
		NumGoroutines:   runtime.GOMAXPROCS(0),
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}
	for b.Loop() {
		if err := gopar.Create(data+".par2", []string{data}, opts); err != nil {
			b.Fatal(err)
		}
	}
}

package par2

// Peak-RSS measurement of the streaming create (plans/streaming_file.md
// §6.7): creating the PAR2 set of a large file must stay under a bound that
// does not grow with the file size — only with the slice size (the parity
// buffers alone are parityShards × sliceByteCount, the same floor gopar
// has). The stage 2 criterion — "create ≤ 150 MiB of RSS" — is about the
// create's own footprint, so the assertion is peak − baseline: the test
// binary alone already maps ~75 MiB resident on darwin before doing
// anything.
//
// The measurement runs in a helper child process that does nothing but the
// create: the corpus data file is written by the parent with constant
// memory. Same scheme as TestVerifyStreamRSS; skipped with -short, the
// corpus size is controlled with PARTUI_RSS_TEST_BYTES (default 256 MiB,
// the documented plan measurement used 8 GiB).

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const createRSSLimitBytes = 150 << 20

func TestCreateStreamRSS(t *testing.T) {
	if testing.Short() {
		t.Skip("large-file memory test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("ps-based sampling")
	}

	// the default is a smoke test for the bound; the documented plan
	// measurements use PARTUI_RSS_TEST_BYTES=8589934592 (8 GiB)
	var corpusSize int64 = 256 << 20
	if v := os.Getenv("PARTUI_RSS_TEST_BYTES"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		require.NoError(t, err)
		corpusSize = parsed
	}

	// fixture: the data file is written streaming, so the parent never holds
	// the corpus
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.bin")
	f, err := os.Create(dataPath)
	require.NoError(t, err)
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i)
	}
	for written := int64(0); written < corpusSize; written += int64(len(chunk)) {
		_, err := f.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	// child: the same test binary, running only the create
	cmd := exec.Command(os.Args[0], "-test.run=^TestCreateStreamRSSChild$")
	cmd.Env = append(os.Environ(), "PARTUI_CREATE_PARPATH="+dataPath+".par2")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	var baselineKB, peakKB int64
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if v, ok := strings.CutPrefix(line, "RSS_BASELINE_KB="); ok {
			baselineKB, _ = strconv.ParseInt(v, 10, 64)
		} else if v, ok := strings.CutPrefix(line, "RSS_PEAK_KB="); ok {
			peakKB, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	require.NoError(t, scanner.Err(), "reading the child output")
	require.NoError(t, cmd.Wait(), "the create child must succeed")

	require.NotZero(t, peakKB, "the child must report a peak")
	t.Logf("corpus %d MiB: child baseline %.1f MiB, peak %.1f MiB, create footprint %.1f MiB",
		corpusSize>>20, float64(baselineKB*1024)/(1<<20), float64(peakKB*1024)/(1<<20),
		float64((peakKB-baselineKB)*1024)/(1<<20))

	createFootprint := (peakKB - baselineKB) * 1024
	require.Less(t, createFootprint, int64(createRSSLimitBytes),
		"the create footprint of a large file must stay under the stage 2 bound")
}

// TestCreateStreamRSSChild is not run directly; TestCreateStreamRSS re-execs
// the test binary with PARTUI_CREATE_PARPATH set.
func TestCreateStreamRSSChild(t *testing.T) {
	parPath := os.Getenv("PARTUI_CREATE_PARPATH")
	if parPath == "" {
		t.Skip("helper process for TestCreateStreamRSS")
	}
	dataPath := strings.TrimSuffix(parPath, ".par2")
	info, err := os.Stat(dataPath)
	require.NoError(t, err)

	self := strconv.Itoa(os.Getpid())
	rssKB := func() int64 {
		out, err := exec.Command("ps", "-o", "rss=", "-p", self).Output()
		if err != nil {
			return 0
		}
		kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return 0
		}
		return kb
	}

	baseline := rssKB()

	var mu sync.Mutex
	peak := baseline
	stop := make(chan struct{})
	go func() {
		for {
			if kb := rssKB(); kb > 0 {
				mu.Lock()
				if kb > peak {
					peak = kb
				}
				mu.Unlock()
			}
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}()

	err = CreateStream(context.Background(), parPath, []string{dataPath}, CreateOptions{
		SliceByteCount:  CalculateBlockSize(uint64(info.Size()), 5),
		NumParityShards: -1,
		NumGoroutines:   0,
	})
	close(stop)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	// direct stdout markers: the parent parses these, t.Log would prefix them
	if _, err := fmt.Fprintf(os.Stdout, "RSS_BASELINE_KB=%d\n", baseline); err != nil {
		t.Fatalf("writing the baseline marker: %v", err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "RSS_PEAK_KB=%d\n", peak); err != nil {
		t.Fatalf("writing the peak marker: %v", err)
	}
}

package par2

// Peak-RSS measurement of the streaming verify (plans/streaming_file.md
// §5.6): verifying a large file must stay under a small, file-size-
// independent bound. The stage 1 criterion — "verify ≤ 100 MiB of RSS,
// regardless of the file size" — is about the verify's own footprint, so
// the assertion is peak − baseline: the test binary alone already maps
// ~75 MiB resident on darwin before doing anything.
//
// The measurement runs in a helper child process that does nothing but the
// verify: the fixture lives in the parent, so the child never holds the
// corpus. That also sidesteps the macOS accounting quirk where madvise-
// scavenged pages keep counting towards the resident size of the process
// that freed them. Skipped with -short; the corpus size is controlled with
// PARTUI_RSS_TEST_BYTES (default 256 MiB, the documented plan measurement
// used 8 GiB).

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gopar "github.com/akalin/gopar/par2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rssLimitBytes = 100 << 20

// craftedAboveBytes splits the fixture strategies: corpora above it use the
// streamed hand-crafted set, because gopar would hold ~2× the corpus in the
// parent process.
const craftedAboveBytes = 2 << 30

// corpusFixture builds the corpus and its PAR2 set in the parent process
// with constant memory. Small corpora go through gopar (the real
// generator); for the multi-GiB documented runs the set is written directly
// with the packet primitives — TestRSSCraftedFixture proves the crafted
// path verifies as healthy, and the verify streams the data file either
// way. Recovery volumes are omitted (parity=0 leaves the statuses intact).
func corpusFixture(t *testing.T, dir string, size int64) string {
	t.Helper()
	if size <= craftedAboveBytes {
		return goparFixture(t, dir, size)
	}
	return craftedFixture(t, dir, size)
}

func goparFixture(t *testing.T, dir string, size int64) string {
	t.Helper()
	dataPath := dir + "/data.bin"
	parPath := dataPath + ".par2"

	slice := CalculateBlockSize(uint64(size), 5)

	f, err := os.Create(dataPath)
	require.NoError(t, err)
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i)
	}
	for written := int64(0); written < size; written += int64(len(chunk)) {
		_, err := f.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	require.NoError(t, gopar.Create(parPath, []string{dataPath}, gopar.CreateOptions{
		SliceByteCount:  slice,
		NumParityShards: -1,
		NumGoroutines:   1,
		CreateDelegate:  gopar.DoNothingCreateDelegate{},
	}))
	return parPath
}

// craftedFixture writes a large corpus plus its PAR2 index directly with
// the packet primitives: one streaming pass over the data collects the
// file hashes and the per-slice checksum pairs, memory stays constant.
// Slice boundaries are file-global — fragments are an internal detail.
func craftedFixture(t *testing.T, dir string, size int64) string {
	t.Helper()
	dataPath := dir + "/data.bin"
	parPath := dataPath + ".par2"

	slice := CalculateBlockSize(uint64(size), 5)

	// streamed fixture: one pass over the data, checksum pairs collected
	// on the way, then the index packets written
	const name = "data.bin"
	setID := recoverySetID{0x5A}
	state := uint64(0x9E3779B97F4A7C15)
	fragment := make([]byte, 1<<20)
	next := func() byte {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return byte(state >> 24)
	}

	df, err := os.Create(dataPath)
	require.NoError(t, err)
	pf, err := os.Create(parPath)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, pf.Close())
	}()

	writePacket := func(ptype packetType, body []byte) {
		if pad := len(body) % 4; pad != 0 {
			body = append(body, make([]byte, 4-pad)...)
		}
		var header bytes.Buffer
		_ = binary.Write(&header, binary.LittleEndian, expectedMagic)
		_ = binary.Write(&header, binary.LittleEndian, uint64(headerSize+len(body)))
		h := md5.New()
		h.Write(setID[:])
		h.Write(ptype[:])
		h.Write(body)
		header.Write(h.Sum(nil))
		header.Write(setID[:])
		header.Write(ptype[:])
		_, err := pf.Write(header.Bytes())
		require.NoError(t, err)
		_, err = pf.Write(body)
		require.NoError(t, err)
	}

	// the creator packet comes first; main, description and IFSC follow
	// once the streamed hashes are known
	writePacket(creatorPacketType, []byte("partui-rss-test"))

	var fileHash, sixteenK hash.Hash = md5.New(), md5.New()
	sixteenKLeft := 16 << 10
	var pairs []checksumPair
	win := make([]byte, slice)
	winFill := 0 // bytes of the current file-global slice accumulated in win
	recordPair := func() {
		var crcBytes [4]byte
		binary.LittleEndian.PutUint32(crcBytes[:], crc32.ChecksumIEEE(win))
		pairs = append(pairs, checksumPair{MD5: md5.Sum(win), CRC32: crcBytes})
	}

	for offset := int64(0); offset < size; {
		fill := min(int64(len(fragment)), size-offset)
		for i := int64(0); i < fill; i++ {
			fragment[i] = next()
		}
		_, err := df.Write(fragment[:fill])
		require.NoError(t, err)

		fileHash.Write(fragment[:fill])
		if sixteenKLeft > 0 {
			sk := min(sixteenKLeft, int(fill))
			sixteenK.Write(fragment[:sk])
			sixteenKLeft -= sk
		}
		// checksum pairs over the file-global slice grid: fragments are
		// 1 MiB, a slice can span fragments, so accumulation carries over
		for pos := int64(0); pos < fill; {
			take := min(int64(slice-winFill), fill-pos)
			copy(win[winFill:], fragment[pos:pos+take])
			winFill += int(take)
			pos += take
			if winFill == slice {
				recordPair()
				winFill = 0
			}
		}
		offset += fill
	}
	if winFill > 0 {
		// the trailing partial slice, zero-padded like the real format
		clear(win[winFill:])
		recordPair()
		winFill = 0
	}
	require.NoError(t, df.Close())

	var gotFile, got16 [md5.Size]byte
	fileHash.Sum(gotFile[:0])
	sixteenK.Sum(got16[:0])
	id := computeFileID(got16, uint64(size), []byte(name))

	var mainBody bytes.Buffer
	_ = binary.Write(&mainBody, binary.LittleEndian, mainPacketHeader{
		SliceSize:        uint64(slice),
		RecoverySetCount: 1,
	})
	mainBody.Write(id[:])
	writePacket(mainPacketType, mainBody.Bytes())

	var descBody bytes.Buffer
	_ = binary.Write(&descBody, binary.LittleEndian, fileDescriptionPacketHeader{
		FileID:       id,
		Hash:         gotFile,
		SixteenKHash: got16,
		Length:       uint64(size),
	})
	descBody.WriteString(name)
	writePacket(fileDescriptionPacketType, descBody.Bytes())

	var ifscBody bytes.Buffer
	ifscBody.Write(id[:])
	for _, p := range pairs {
		require.NoError(t, binary.Write(&ifscBody, binary.LittleEndian, p))
	}
	writePacket(ifscPacketType, ifscBody.Bytes())

	return parPath
}

func TestVerifyStreamRSS(t *testing.T) {
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

	// fixture: built here in the parent with constant memory, so its peak
	// never reaches the measured child
	parPath := corpusFixture(t, t.TempDir(), corpusSize)

	// child: the same test binary, running only the verify
	cmd := exec.Command(os.Args[0], "-test.run=^TestVerifyStreamRSSChild$")
	cmd.Env = append(os.Environ(), "PARTUI_RSS_PARPATH="+parPath)
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
	require.NoError(t, cmd.Wait(), "the verify child must succeed")

	require.NotZero(t, peakKB, "the child must report a peak")
	t.Logf("corpus %d MiB: child baseline %.1f MiB, peak %.1f MiB, verify footprint %.1f MiB",
		corpusSize>>20, float64(baselineKB*1024)/(1<<20), float64(peakKB*1024)/(1<<20),
		float64((peakKB-baselineKB)*1024)/(1<<20))

	verifyFootprint := (peakKB - baselineKB) * 1024
	require.Less(t, verifyFootprint, int64(rssLimitBytes),
		"the verify footprint of a large file must stay under the stage 1 bound")
}

// TestVerifyStreamRSSChild is not run directly; TestVerifyStreamRSS re-execs
// the test binary with PARTUI_RSS_PARPATH set.
func TestVerifyStreamRSSChild(t *testing.T) {
	if os.Getenv("PARTUI_RSS_PARPATH") == "" {
		t.Skip("helper process for TestVerifyStreamRSS")
	}
	parPath := os.Getenv("PARTUI_RSS_PARPATH")

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

	results, err := VerifyStream(context.Background(), parPath, nil)
	close(stop)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}

	mu.Lock()
	defer mu.Unlock()
	// direct stdout markers: the parent parses these, t.Log would prefix them
	fmt.Fprintf(os.Stdout, "RSS_BASELINE_KB=%d\n", baseline)
	fmt.Fprintf(os.Stdout, "RSS_PEAK_KB=%d\n", peak)
}

// TestRSSCraftedFixture guards the streamed fixture of the multi-GiB runs:
// a hand-crafted set must verify as healthy (all slices usable) even though
// slices cross the internal 1 MiB fragment boundaries.
func TestRSSCraftedFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("fixture generation writes ~5 MiB and parses it back")
	}
	parPath := craftedFixture(t, t.TempDir(), 5_000_000)
	results, err := VerifyStream(context.Background(), parPath, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Zero(t, results[0].UnusableShards)
	assert.False(t, results[0].RepairNeeded)
}

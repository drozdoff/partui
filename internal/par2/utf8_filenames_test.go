package par2

// Tests of non-ASCII (UTF-8) file names (plans/utf8_filenames.md §5): the
// byte roundtrip through the description packet, the golden packet written
// by par2cmdline 1.0.0, the par2cmdline cross-oracle (gopar rejects these
// names and cannot be the oracle), and the volume glob fix for names with
// metacharacters.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileDescriptionPacket_UTF8Roundtrip(t *testing.T) {
	names := []string{
		"тест-αβγ.txt",     // cyrillic + greek
		"中文档案.bin",         // CJK
		"🎬 movie.bin",      // emoji + spaces
		"e\u0301clair.bin", // NFD: e + combining acute
		"отчёт №5.bin",     // cyrillic + digits + spaces
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			sixteenK := md5.Sum([]byte(name))
			const size = testSlice
			id := computeFileID(sixteenK, size, []byte(name))

			body, err := writeFileDescriptionPacket(id, fileDescriptionPacket{
				hash:         md5.Sum([]byte("body")),
				sixteenKHash: sixteenK,
				byteCount:    size,
				filename:     name,
			})
			require.NoError(t, err)

			// the body carries the name bytes as-is, no transcoding
			assert.Equal(t, []byte(name), body[56:],
				"the body must carry the name bytes raw")

			gotID, got, err := readFileDescriptionPacket(body)
			require.NoError(t, err)
			assert.Equal(t, id, gotID)
			assert.Equal(t, name, got.filename)
			assert.Equal(t, []byte(name), []byte(got.filename))
		})
	}
}

// par2cmdlineGoldenBodyHex is the file description packet body of an index
// written by par2cmdline 1.0.0 (darwin/arm64) for a 100000-byte file named
// тест-αβγ.txt, extracted byte-for-byte on 07.10.2026
// (plans/utf8_filenames.md §2). The file ID is derived over the name bytes
// without the null terminator; the body itself carries the name
// null-terminated, raw UTF-8.
const par2cmdlineGoldenBodyHex = "93f7fb4bc541568681b24dafb618a36e" + // file ID
	"30f14e18215755f7c233dff8c67f61fc" + // md5 of the data file
	"2d10ad214a14b8d52e3d0b5cdcddfaef" + // sixteenK hash
	"a086010000000000" + // byte count, little-endian 100000
	"d182d0b5d181d1822dceb1ceb2ceb32e74787400" // "тест-αβγ.txt\x00"

func TestReadFileDescriptionPacket_Par2CmdlineGolden(t *testing.T) {
	body, err := hex.DecodeString(par2cmdlineGoldenBodyHex)
	require.NoError(t, err)

	id, desc, err := readFileDescriptionPacket(body)
	require.NoError(t, err)

	wantID := fileID{0x93, 0xf7, 0xfb, 0x4b, 0xc5, 0x41, 0x56, 0x86, 0x81, 0xb2,
		0x4d, 0xaf, 0xb6, 0x18, 0xa3, 0x6e}
	assert.Equal(t, wantID, id)
	assert.Equal(t, "тест-αβγ.txt", desc.filename)
	assert.Equal(t, 100000, desc.byteCount)

	var sixteenK [md5.Size]byte
	copy(sixteenK[:], body[32:48])
	assert.Equal(t, sixteenK, desc.sixteenKHash)

	// the derivation itself: file ID = md5(sixteenK ‖ le64(length) ‖ name
	// without the null) — what computeFileID plus the reader's
	// nullTerminate produce, independent of the vendored reader
	recomputed := computeFileID(sixteenK, 100000, []byte("тест-αβγ.txt"))
	assert.Equal(t, wantID, recomputed)
}

func TestReadFileDescriptionPacket_NonUTF8(t *testing.T) {
	// "отчёт.bin" in CP1251 — bytes a Linux par2cmdline could have stored
	// byte-for-byte; the reader keeps them instead of transcoding
	name := string([]byte{0xEE, 0xF2, 0xF7, 0xB8, 0xF2, '.', 'b', 'i', 'n'})
	sixteenK := md5.Sum([]byte("x"))
	const size = uint64(16)
	id := computeFileID(sixteenK, size, []byte(name))
	body := craftDescriptionBody(id, md5.Sum([]byte("y")), sixteenK, size, name)

	gotID, desc, err := readFileDescriptionPacket(body)
	require.NoError(t, err)
	assert.Equal(t, id, gotID)
	assert.Equal(t, name, desc.filename)
}

func TestCreateVerifyStream_UTF8(t *testing.T) {
	dir := t.TempDir()
	name := "тест-αβγ.txt"
	data := writeInputFile(t, dir, name, 5*testSlice+3, 7)
	parPath := data + ".par2"

	ctx := context.Background()
	require.NoError(t, CreateStream(ctx, parPath, []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: 3,
		NumGoroutines:   1,
	}))

	names, err := ListPar2Filenames(parPath)
	require.NoError(t, err)
	assert.Equal(t, []string{name}, names)

	results, err := VerifyStream(ctx, parPath, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, data, results[0].Path)
	assert.False(t, results[0].Missing)
	assert.False(t, results[0].RepairNeeded)
}

func TestLoadParity_GlobMetachars(t *testing.T) {
	t.Run("volumes of report[1].txt are found", func(t *testing.T) {
		dir := t.TempDir()
		data := writeFixture(t, dir, "report[1].txt", 2*testSlice, 11)

		meta, err := parseParSet(data + ".par2")
		require.NoError(t, err)
		require.NoError(t, meta.loadParity(data+".par2"))
		assert.Equal(t, 3, meta.parityExponents,
			"the volume of a name with a character class must be found")
	})

	t.Run("a[0].txt does not swallow a0.txt volumes", func(t *testing.T) {
		dir := t.TempDir()
		a0 := writeFixture(t, dir, "a0.txt", 2*testSlice, 12)
		bracket := writeFixture(t, dir, "a[0].txt", 2*testSlice, 13)

		meta, err := parseParSet(bracket + ".par2")
		require.NoError(t, err)
		require.NoError(t, meta.loadParity(bracket+".par2"))
		assert.Equal(t, 3, meta.parityExponents)

		metaOther, err := parseParSet(a0 + ".par2")
		require.NoError(t, err)
		require.NoError(t, metaOther.loadParity(a0+".par2"))
		assert.Equal(t, 3, metaOther.parityExponents)
	})
}

// requirePar2Oracle skips unless the par2cmdline binary is available — the
// only possible cross-oracle for UTF-8 names, since gopar rejects them
// (plans/utf8_filenames.md §5).
func requirePar2Oracle(t *testing.T) {
	if testing.Short() {
		t.Skip("par2cmdline oracle skipped in -short")
	}
	if _, err := exec.LookPath("par2"); err != nil {
		t.Skip("par2cmdline is not installed")
	}
}

// runPar2 runs the par2 binary in dir and returns its exit code: 0 = OK,
// 1 = repairable damage, 2 = unrecoverable damage or error.
func runPar2(t *testing.T, dir string, args ...string) int {
	t.Helper()
	cmd := exec.Command("par2", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Logf("par2 %v:\n%s", args, out)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	require.NoError(t, err)
	return 0
}

func TestPar2Cmdline_VerifiesOurUTF8Output(t *testing.T) {
	requirePar2Oracle(t)
	dir := t.TempDir()
	name := "тест-αβγ.txt"
	data := writeInputFile(t, dir, name, 5*testSlice, 21)
	parPath := data + ".par2"

	require.NoError(t, CreateStream(context.Background(), parPath, []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: 3,
		NumGoroutines:   1,
	}))

	assert.Equal(t, 0, runPar2(t, dir, "v", "-q", name+".par2"),
		"par2cmdline must verify what CreateStream wrote for a UTF-8 name")
}

func TestPar2Cmdline_OutputVerifiedByUs(t *testing.T) {
	requirePar2Oracle(t)
	dir := t.TempDir()
	name := "中文 файл.bin"
	parName := name + ".par2"
	writeInputFile(t, dir, name, 5*testSlice, 22)

	assert.Equal(t, 0, runPar2(t, dir, "c", "-q", "-r5", parName, name))

	parPath := filepath.Join(dir, parName)
	names, err := ListPar2Filenames(parPath)
	require.NoError(t, err)
	assert.Equal(t, []string{name}, names)

	results, err := VerifyStream(context.Background(), parPath, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Missing)
	assert.False(t, results[0].RepairNeeded)
}

func TestPar2Cmdline_DamagedAgreement(t *testing.T) {
	requirePar2Oracle(t)
	dir := t.TempDir()
	name := "тест-αβγ.txt"
	data := writeInputFile(t, dir, name, 10*testSlice, 23)
	parPath := data + ".par2"

	require.NoError(t, CreateStream(context.Background(), parPath, []string{data}, CreateOptions{
		SliceByteCount:  testSlice,
		NumParityShards: 3,
		NumGoroutines:   1,
	}))

	// damage one byte in the middle slice, keeping the size
	bs, err := os.ReadFile(data)
	require.NoError(t, err)
	bs[5*testSlice+7] ^= 0xFF
	require.NoError(t, os.WriteFile(data, bs, 0o644))

	assert.Equal(t, 1, runPar2(t, dir, "v", "-q", name+".par2"),
		"par2cmdline must see the damage too")

	results, err := VerifyStream(context.Background(), parPath, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].RepairNeeded)
	assert.False(t, results[0].Missing)
}

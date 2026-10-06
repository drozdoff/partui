package par2

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// craftPacket lays out a packet byte-for-byte by hand — an implementation
// independent of the vendored reader, so the framing tests are golden. The
// body is zero-padded to a multiple of four first, the way the format
// requires (gopar's padPacketBytes); the hash covers the padded body.
func craftPacket(setID recoverySetID, ptype packetType, body []byte, hashBody bool) []byte {
	if pad := len(body) % 4; pad != 0 {
		body = append(body, make([]byte, 4-pad)...)
	}
	var buf bytes.Buffer
	buf.Write(expectedMagic[:])
	length := uint64(headerSize + len(body))
	_ = binaryWrite(&buf, length)
	if hashBody {
		h := md5.New()
		h.Write(setID[:])
		h.Write(ptype[:])
		h.Write(body)
		buf.Write(h.Sum(nil))
	} else {
		buf.Write(make([]byte, md5.Size))
	}
	buf.Write(setID[:])
	buf.Write(ptype[:])
	buf.Write(body)
	return buf.Bytes()
}

func binaryWrite(buf *bytes.Buffer, v uint64) error {
	return binary.Write(buf, binary.LittleEndian, v)
}

func TestReadNextPacket_Golden(t *testing.T) {
	setID := recoverySetID{1, 2, 3, 4}
	ptype := creatorPacketType
	// the reader sees the padded body: the format zero-pads packet bodies
	// to a multiple of four
	body := []byte("gopar\x00\x00\x00")

	raw := craftPacket(setID, ptype, []byte("gopar"), true)
	gotSetID, gotType, gotBody, err := readNextPacket(bytes.NewBuffer(raw))
	require.NoError(t, err)
	assert.Equal(t, setID, gotSetID)
	assert.Equal(t, packetType(ptype), gotType)
	assert.Equal(t, body, gotBody)
}

func TestReadNextPacket_Errors(t *testing.T) {
	setID := recoverySetID{9}
	ptype := recoveryPacketType
	body := []byte{0, 0, 0, 0, 0xAA, 0xBB, 0xCC, 0xDD}
	good := craftPacket(setID, ptype, body, true)

	t.Run("bad magic", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[3] = 'X'
		_, _, _, err := readNextPacket(bytes.NewBuffer(bad))
		assert.EqualError(t, err, "unexpected magic string")
	})

	t.Run("length below header size", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint64(bad[8:], headerSize-4)
		_, _, _, err := readNextPacket(bytes.NewBuffer(bad))
		assert.EqualError(t, err, "invalid length")
	})

	t.Run("length not multiple of four", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint64(bad[8:], headerSize+2)
		_, _, _, err := readNextPacket(bytes.NewBuffer(bad))
		assert.EqualError(t, err, "invalid length")
	})

	t.Run("corrupted body", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[len(bad)-1] ^= 0xFF
		_, _, _, err := readNextPacket(bytes.NewBuffer(bad))
		assert.EqualError(t, err, "hash mismatch")
	})

	t.Run("truncated body", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint64(bad[8:], uint64(headerSize+len(body)+4))
		_, _, _, err := readNextPacket(bytes.NewBuffer(bad))
		assert.EqualError(t, err, "could not read body")
	})

	t.Run("huge length does not panic", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint64(bad[8:], 1<<40)
		_, _, _, err := readNextPacket(bytes.NewBuffer(bad))
		assert.EqualError(t, err, "could not read body")
	})

	t.Run("empty stream is io.EOF", func(t *testing.T) {
		_, _, _, err := readNextPacket(bytes.NewBuffer(nil))
		assert.True(t, errors.Is(err, io.EOF))
	})
}

func TestReadPackets_MultiPacketStream(t *testing.T) {
	setID := recoverySetID{7}
	id := fileID{1}
	packets := bytes.NewBuffer(nil)
	packets.Write(craftPacket(setID, creatorPacketType, []byte("gopar"), true))
	packets.Write(craftPacket(setID, mainPacketType, craftMainBody(testSlice, []fileID{id}, nil), true))
	// a packet of another set: skipped when the set ID is pinned, adopted
	// otherwise
	otherID := recoverySetID{42}
	packets.Write(craftPacket(otherID, creatorPacketType, []byte("other"), true))

	t.Run("pinned set skips foreign packets", func(t *testing.T) {
		gotSetID, f, err := readPackets(packets.Bytes(), &setID)
		require.NoError(t, err)
		assert.Equal(t, setID, gotSetID)
		require.NotNil(t, f.mainPacket)
		assert.Equal(t, testSlice, f.mainPacket.sliceByteCount)
		assert.Equal(t, []fileID{id}, f.mainPacket.recoverySet)
	})

	t.Run("unpinned adopts the first packet set", func(t *testing.T) {
		// only the creator packet of the first set, without a main packet
		onlyCreator := craftPacket(setID, creatorPacketType, []byte("gopar"), true)
		gotSetID, f, err := readPackets(onlyCreator, nil)
		require.NoError(t, err)
		assert.Equal(t, setID, gotSetID)
		assert.Nil(t, f.mainPacket)
	})
}

func TestReadPackets_Errors(t *testing.T) {
	setID := recoverySetID{7}

	t.Run("no packets at all", func(t *testing.T) {
		_, _, err := readPackets(nil, &setID)
		assert.ErrorIs(t, err, errNoPacketsFound)
	})

	t.Run("only foreign-set packets", func(t *testing.T) {
		other := recoverySetID{8}
		raw := craftPacket(other, creatorPacketType, []byte("other"), true)
		_, _, err := readPackets(raw, &setID)
		assert.ErrorIs(t, err, errNoPacketsFound)
	})

	t.Run("matching creator but no main packet", func(t *testing.T) {
		raw := craftPacket(setID, creatorPacketType, []byte("gopar"), true)
		_, f, err := readPackets(raw, nil)
		// readPackets itself accepts a main-packet-less file; parseParSet
		// turns that into "no main packet found" (see verify_test.go)
		require.NoError(t, err)
		assert.Nil(t, f.mainPacket)
		assert.Equal(t, "gopar", f.clientID)
	})

	t.Run("packets but no creator", func(t *testing.T) {
		raw := craftPacket(setID, mainPacketType, craftMainBody(testSlice, []fileID{{1}}, nil), true)
		_, _, err := readPackets(raw, nil)
		assert.EqualError(t, err, "no creator packet found")
	})

	t.Run("duplicate recovery exponent with differing contents", func(t *testing.T) {
		raw := bytes.NewBuffer(nil)
		raw.Write(craftPacket(setID, creatorPacketType, []byte("gopar"), true))
		raw.Write(craftPacket(setID, recoveryPacketType, []byte{0, 0, 0, 0, 1, 2, 3, 4}, true))
		raw.Write(craftPacket(setID, recoveryPacketType, []byte{0, 0, 0, 0, 5, 6, 7, 8}, true))
		_, _, err := readPackets(raw.Bytes(), nil)
		assert.EqualError(t, err, "recovery packet with duplicate exponent but differing contents")
	})
}

// craftMainBody builds a main packet body: header, then the file IDs.
// bytes.Buffer writes cannot fail, so the errors are ignored.
func craftMainBody(sliceSize uint64, recoverySet, nonRecoverySet []fileID) []byte {
	var buf bytes.Buffer
	_ = binaryWrite(&buf, sliceSize)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(recoverySet)))
	for _, id := range recoverySet {
		buf.Write(id[:])
	}
	for _, id := range nonRecoverySet {
		buf.Write(id[:])
	}
	return buf.Bytes()
}

func TestCRC32Window_EqFullCRC(t *testing.T) {
	// Property: shifting the window by one byte with a constant-time
	// update equals recomputing crc32.ChecksumIEEE over the new window.
	for _, n := range []int{4, 5, 7, 64, 4096} {
		w := newCRC32Window(n)
		rng := rand.New(rand.NewSource(int64(n)))
		data := make([]byte, 3*n+17)
		rng.Read(data)

		crc := crc32.ChecksumIEEE(data[:n])
		assert.Equal(t, crc32.ChecksumIEEE(data[:n]), crc)
		for j := 1; j+n <= len(data); j++ {
			crc = w.update(crc, data[j-1], data[j+n-1])
			require.Equal(t, crc32.ChecksumIEEE(data[j:j+n]), crc, "window at %d (n=%d)", j, n)
		}
	}
}

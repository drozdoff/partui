// Vendored from github.com/akalin/gopar (package par2), revision
// a776223763d3 (v0.0.0-20210524065929), as the packet layer of the
// streaming PAR2 implementation (plans/streaming_file.md §5). Original code:
//
//	Copyright (c) 2017, Frederick Akalin
//	All rights reserved.
//
//	Redistribution and use in source and binary forms, with or without
//	modification, are permitted provided that the following conditions are met:
//	    * Redistributions of source code must retain the above copyright
//	      notice, this list of conditions and the following disclaimer.
//	    * Redistributions in binary form must reproduce the above copyright
//	      notice, this list of conditions and the following disclaimer in the
//	      documentation and/or other materials provided with the distribution.
//	    * Neither the name of the copyright holder nor the
//	      names of its contributors may be used to endorse or promote products
//	      derived from this software without specific prior written permission.
//
//	THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
//	AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
//	IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
//	ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
//	LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
//	CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
//	SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
//	INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
//	CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
//	ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
//	POSSIBILITY OF SUCH DAMAGE.
//
// Adaptations against the original:
//   - the delegate callbacks of gopar's readFile are dropped; readPackets
//     returns the parsed packets and skips unknown ones;
//   - the packet header size is a constant instead of a reflect call;
//   - a packet length that would not fit the remaining bytes aborts with
//     "could not read body" instead of panicking inside bytes.Buffer.Next
//     (gopar panics on lengths that overflow int);
//   - recovery packet bodies are kept whole instead of being split into
//     exponent + data — verification never uses the shard bytes.

package par2

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"path"
	"unicode"
	"unicode/utf8"
)

// headerSize is the size of the fixed part of a PAR2 packet: magic (8) +
// length (8) + md5 hash (16) + recovery set ID (16) + packet type (16).
const headerSize = 64

// fileIDSize and checksumPairSize are the on-wire sizes gopar took from
// reflect: a file ID is 16 raw bytes, a checksum pair is md5 (16) + crc32 (4).
const (
	fileIDSize       = 16
	checksumPairSize = 20
)

type recoverySetID [16]byte
type packetType [16]byte
type fileID [16]byte

type packetHeader struct {
	Magic         [8]byte
	Length        uint64
	Hash          [16]byte
	RecoverySetID [16]byte
	Type          [16]byte
}

var expectedMagic = [8]byte{'P', 'A', 'R', '2', '\x00', 'P', 'K', 'T'}

func checkPacketHeader(h packetHeader) error {
	if h.Magic != expectedMagic {
		return errors.New("unexpected magic string")
	}

	if h.Length < headerSize || h.Length%4 != 0 {
		return errors.New("invalid length")
	}

	return nil
}

func readPacketHeader(buf *bytes.Buffer) (packetHeader, error) {
	var h packetHeader
	err := binary.Read(buf, binary.LittleEndian, &h)
	if err != nil {
		return packetHeader{}, err
	}

	err = checkPacketHeader(h)
	if err != nil {
		return packetHeader{}, err
	}

	return h, nil
}

func computePacketHash(setID recoverySetID, ptype packetType, body []byte) [md5.Size]byte {
	var hashInput []byte
	hashInput = append(hashInput, setID[:]...)
	hashInput = append(hashInput, ptype[:]...)
	hashInput = append(hashInput, body...)
	return md5.Sum(hashInput)
}

func readNextPacket(buf *bytes.Buffer) (recoverySetID, packetType, []byte, error) {
	h, err := readPacketHeader(buf)
	if err != nil {
		return recoverySetID{}, packetType{}, nil, err
	}

	bodyLength := int64(h.Length) - headerSize
	if bodyLength < 0 || bodyLength > int64(buf.Len()) {
		return recoverySetID{}, packetType{}, nil, errors.New("could not read body")
	}
	body := buf.Next(int(bodyLength))

	if computePacketHash(h.RecoverySetID, h.Type, body) != h.Hash {
		return recoverySetID{}, packetType{}, nil, errors.New("hash mismatch")
	}

	bodyCopy := make([]byte, len(body))
	copy(bodyCopy, body)
	return h.RecoverySetID, h.Type, bodyCopy, nil
}

// --- main packet -----------------------------------------------------------

var mainPacketType = packetType{'P', 'A', 'R', ' ', '2', '.', '0', '\x00', 'M', 'a', 'i', 'n'}

type mainPacketHeader struct {
	SliceSize        uint64
	RecoverySetCount uint32
}

func fileIDLess(id1, id2 fileID) bool {
	for i := len(id1) - 1; i >= 0; i-- {
		if id1[i] < id2[i] {
			return true
		} else if id1[i] > id2[i] {
			return false
		}
	}

	return false
}

type mainPacket struct {
	sliceByteCount int
	recoverySet    []fileID
	nonRecoverySet []fileID
}

func checkFileIDSetsSorted(recoverySet, nonRecoverySet []fileID) error {
	if !isSortedByFileID(recoverySet) {
		return errors.New("recovery set IDs not sorted")
	}

	if !isSortedByFileID(nonRecoverySet) {
		return errors.New("non-recovery set IDs not sorted")
	}

	return nil
}

func isSortedByFileID(ids []fileID) bool {
	for i := 1; i < len(ids); i++ {
		if fileIDLess(ids[i], ids[i-1]) {
			return false
		}
	}
	return true
}

func readMainPacket(body []byte) (mainPacket, error) {
	buf := bytes.NewBuffer(body)

	var h mainPacketHeader
	err := binary.Read(buf, binary.LittleEndian, &h)
	if err != nil {
		return mainPacket{}, err
	}

	maxInt := uint64(^uint(0) >> 1)
	if h.SliceSize == 0 || h.SliceSize%4 != 0 || h.SliceSize > maxInt {
		return mainPacket{}, errors.New("invalid slice size")
	}

	sliceByteCount := int(h.SliceSize)

	if h.RecoverySetCount == 0 {
		return mainPacket{}, errors.New("empty recovery set")
	}

	if buf.Len()%fileIDSize != 0 {
		return mainPacket{}, errors.New("invalid size")
	}
	fileIDs := make([]fileID, buf.Len()/fileIDSize)
	err = binary.Read(buf, binary.LittleEndian, fileIDs)
	if err != nil {
		return mainPacket{}, err
	}

	if uint64(len(fileIDs)) < uint64(h.RecoverySetCount) {
		return mainPacket{}, errors.New("not enough file IDs")
	}

	recoverySet := fileIDs[:int(h.RecoverySetCount)]
	nonRecoverySet := fileIDs[int(h.RecoverySetCount):]

	err = checkFileIDSetsSorted(recoverySet, nonRecoverySet)
	if err != nil {
		return mainPacket{}, err
	}

	return mainPacket{sliceByteCount, recoverySet, nonRecoverySet}, nil
}

// --- file description packet -----------------------------------------------

var fileDescriptionPacketType = packetType{'P', 'A', 'R', ' ', '2', '.', '0', '\x00', 'F', 'i', 'l', 'e', 'D', 'e', 's', 'c'}

type fileDescriptionPacketHeader struct {
	FileID       [16]byte
	Hash         [16]byte
	SixteenKHash [16]byte
	Length       uint64
}

type fileDescriptionPacket struct {
	hash         [md5.Size]byte
	sixteenKHash [md5.Size]byte
	byteCount    int
	filename     string
}

func computeFileID(sixteenKHash [md5.Size]byte, byteCount uint64, filenameBytes []byte) fileID {
	var hashInput []byte
	hashInput = append(hashInput, sixteenKHash[:]...)
	var byteCountBytes [8]byte
	binary.LittleEndian.PutUint64(byteCountBytes[:], byteCount)
	hashInput = append(hashInput, byteCountBytes[:]...)
	hashInput = append(hashInput, filenameBytes...)
	return md5.Sum(hashInput)
}

func checkFilename(filename string) error {
	// Filenames shouldn't be absolute, to preclude the repair process
	// overwriting arbitrary files.
	if path.IsAbs(filename) {
		return errors.New("absolute paths not allowed")
	}
	filename = path.Clean(filename)
	if filename[0] == '.' {
		return errors.New("traversing outside of the current directory is not allowed")
	}
	return nil
}

func readFileDescriptionPacket(body []byte) (fileID, fileDescriptionPacket, error) {
	buf := bytes.NewBuffer(body)

	var h fileDescriptionPacketHeader
	err := binary.Read(buf, binary.LittleEndian, &h)
	if err != nil {
		return fileID{}, fileDescriptionPacket{}, err
	}

	filenameBytes := buf.Bytes()
	computedFileID := computeFileID(h.SixteenKHash, h.Length, nullTerminate(filenameBytes))
	if computedFileID != h.FileID {
		return fileID{}, fileDescriptionPacket{}, errors.New("file ID mismatch")
	}

	if h.Length == 0 {
		// This isn't specified by the spec, but par2 skips empty files.
		return fileID{}, fileDescriptionPacket{}, errors.New("empty files not allowed")
	}

	filename := decodeNullPaddedASCIIString(filenameBytes)
	err = checkFilename(filename)
	if err != nil {
		return fileID{}, fileDescriptionPacket{}, err
	}

	maxInt := int(^uint(0) >> 1)
	if h.Length > uint64(maxInt) {
		return fileID{}, fileDescriptionPacket{}, errors.New("file length too big")
	}

	byteCount := int(h.Length)
	return h.FileID, fileDescriptionPacket{h.Hash, h.SixteenKHash, byteCount, filename}, nil
}

// --- input file slice checksum (IFSC) packet -------------------------------

var ifscPacketType = packetType{'P', 'A', 'R', ' ', '2', '.', '0', '\x00', 'I', 'F', 'S', 'C'}

type checksumPair struct {
	MD5   [md5.Size]byte
	CRC32 [4]byte
}

type ifscPacket struct {
	checksumPairs []checksumPair
}

func readIFSCPacket(body []byte) (fileID, ifscPacket, error) {
	buf := bytes.NewBuffer(body)

	var id fileID
	err := binary.Read(buf, binary.LittleEndian, &id)
	if err != nil {
		return fileID{}, ifscPacket{}, err
	}

	if buf.Len() == 0 || buf.Len()%checksumPairSize != 0 {
		return fileID{}, ifscPacket{}, errors.New("invalid size")
	}
	checksumPairs := make([]checksumPair, buf.Len()/checksumPairSize)
	err = binary.Read(buf, binary.LittleEndian, checksumPairs)
	if err != nil {
		return fileID{}, ifscPacket{}, err
	}

	return id, ifscPacket{checksumPairs}, nil
}

// --- recovery packet ---------------------------------------------------------

var recoveryPacketType = packetType{'P', 'A', 'R', ' ', '2', '.', '0', '\x00', 'R', 'e', 'c', 'v', 'S', 'l', 'i', 'c'}

type exponent uint16

func readRecoveryPacket(body []byte) (exponent, error) {
	if len(body) == 0 || len(body)%4 != 0 {
		return 0, errors.New("invalid recovery data byte count")
	}

	exp := binary.LittleEndian.Uint32(body)
	if exp > 0xffff {
		return 0, errors.New("exponent out of range")
	}

	return exponent(exp), nil
}

// --- creator packet and strings ----------------------------------------------

var creatorPacketType = packetType{'P', 'A', 'R', ' ', '2', '.', '0', '\x00', 'C', 'r', 'e', 'a', 't', 'o', 'r'}

func readCreatorPacket(body []byte) string {
	return decodeNullPaddedASCIIString(body)
}

func nullTerminate(bs []byte) []byte {
	// This emulates the direction in the spec to append a null byte to
	// turn a string into a null-terminated one.
	for i, b := range bs {
		if b == '\x00' {
			return bs[:i]
		}
	}

	return bs
}

func decodeNullPaddedASCIIString(bs []byte) string {
	// First, null-terminate if necessary.
	bs = nullTerminate(bs)

	var replaceBuf [4]byte
	n := utf8.EncodeRune(replaceBuf[:], unicode.ReplacementChar)

	// Replace all non-ASCII characters with the replacement character.
	var outBytes []byte
	for _, b := range bs {
		if b <= unicode.MaxASCII {
			outBytes = append(outBytes, b)
		} else {
			outBytes = append(outBytes, replaceBuf[:n]...)
		}
	}

	return string(outBytes)
}

// --- whole-file packet walk ---------------------------------------------------

// par2File is the parsed content of one PAR2 file (index or volume). The
// recovery bodies are kept whole: the duplicate-exponent check compares
// them, verification itself never reads the shard bytes.
type par2File struct {
	clientID     string
	mainPacket   *mainPacket
	descriptions map[fileID]fileDescriptionPacket
	ifsc         map[fileID]ifscPacket
	recovery     map[exponent][]byte
}

// errNoPacketsFound is returned by readPackets when the byte stream holds
// no packet at all. For data files of the set this is an error; a volume
// file where every packet belongs to another set is simply not a parity
// volume and is skipped.
var errNoPacketsFound = errors.New("no packets found")

// readPackets walks the packet stream of a PAR2 file. When expectedSetID is
// given (volume files), packets of other sets are skipped; otherwise the
// set ID of the first packet is adopted (index files).
func readPackets(data []byte, expectedSetID *recoverySetID) (recoverySetID, par2File, error) {
	buf := bytes.NewBuffer(data)

	var f par2File
	f.descriptions = make(map[fileID]fileDescriptionPacket)
	f.ifsc = make(map[fileID]ifscPacket)
	f.recovery = make(map[exponent][]byte)

	var setID recoverySetID
	hasSetID := expectedSetID != nil
	if hasSetID {
		setID = *expectedSetID
	}

	var foundPacket, foundClientID bool
	for {
		packetSetID, ptype, body, err := readNextPacket(buf)
		if err == io.EOF {
			break
		} else if err != nil {
			return recoverySetID{}, par2File{}, err
		}
		if hasSetID {
			if packetSetID != setID {
				continue
			}
		} else {
			setID = packetSetID
			hasSetID = true
		}
		foundPacket = true

		switch ptype {
		case creatorPacketType:
			f.clientID = readCreatorPacket(body)
			foundClientID = true

		case mainPacketType:
			mainPacketRead, err := readMainPacket(body)
			if err != nil {
				return recoverySetID{}, par2File{}, err
			}
			f.mainPacket = &mainPacketRead

		case fileDescriptionPacketType:
			fileIDRead, description, err := readFileDescriptionPacket(body)
			if err != nil {
				return recoverySetID{}, par2File{}, err
			}
			f.descriptions[fileIDRead] = description

		case ifscPacketType:
			fileIDRead, ifscRead, err := readIFSCPacket(body)
			if err != nil {
				return recoverySetID{}, par2File{}, err
			}
			f.ifsc[fileIDRead] = ifscRead

		case recoveryPacketType:
			exp, err := readRecoveryPacket(body)
			if err != nil {
				return recoverySetID{}, par2File{}, err
			}
			if existing, ok := f.recovery[exp]; ok && !bytes.Equal(existing, body) {
				return recoverySetID{}, par2File{}, errors.New("recovery packet with duplicate exponent but differing contents")
			}
			f.recovery[exp] = body

		default:
			// Unknown packets carry no verification state.
		}
	}

	if !foundPacket {
		return recoverySetID{}, par2File{}, errNoPacketsFound
	}

	if !foundClientID {
		return recoverySetID{}, par2File{}, errors.New("no creator packet found")
	}

	return setID, f, nil
}

// --- crc32 sliding window ------------------------------------------------------

// crc32Window computes the CRC32 of a window shifted by one byte from the
// CRC32 of the window before the shift, in constant time. Vendored as-is
// from gopar's crc32.go (see the file header for the license).
type crc32Window struct {
	windowSize              int
	crcOldLeaderMaskedTable [256]uint32
}

func newCRC32Window(windowSize int) *crc32Window {
	if windowSize < 4 {
		panic("window size too small")
	}
	a := make([]byte, windowSize+1)
	crc0 := crc32.ChecksumIEEE(a)

	// See comments in crc32Window.update below for why we need
	// crcWindowMask.
	a[0] = 0xff
	a[4] = 0xff
	crcWindowMask := crc32.ChecksumIEEE(a)

	// Set baseTable[i] to crc((1 << i)..) for 0 <= i < 8,
	// where .. denotes padding with trailing 0s to have length
	// windowSize+1.
	a[4] = 0
	var baseTable [8]uint32
	for i := uint(0); i < 8; i++ {
		a[0] = byte(1 << i)
		baseTable[i] = crc32.ChecksumIEEE(a)
	}

	var maskedTable [256]uint32
	// Set maskedTable[i] to crc(i..) ^ crcWindowMask for
	// 0 <= i < 256.
	maskedTable[0] = crc0 ^ crcWindowMask
	for i := 1; i < 256; i++ {
		// Compute crc(i..) using baseTable and the identities
		//
		//   crc(a_1 ^ ... ^ a_n) = crc(a_1) ^ ... ^ crc(a^n)
		//
		// for odd n, and
		//
		//   crc(a_1 ^ ... ^ a_n) =
		//     crc(a_1) ^ ... ^ crc(a^n) ^ crc(0..)
		//
		// for even n, which can be deduced from letting
		//
		//   crc(a) = ffffffff ^ crcz(ffffffff.. ^ a)
		//
		// and the identity
		//
		//   crcz(a ^ b) = crcz(a) ^ crcz(b).
		var crc uint32
		crcCount := 0
		for j := uint(0); j < 8; j++ {
			if i&(1<<j) != 0 {
				crc ^= baseTable[j]
				crcCount++
			}
		}
		if crcCount%2 == 0 {
			crc ^= crc0
		}
		maskedTable[i] = crc ^ crcWindowMask
	}

	return &crc32Window{
		windowSize:              windowSize,
		crcOldLeaderMaskedTable: maskedTable,
	}
}

// update returns the crc (crc32.ChecksumIEEE) of a[1:n+1], given the
// crc of a[0:n], oldLeader = a[0], and newTrailer = a[n], where n is
// the window size, and a[] is a "virtual" byte slice. It does so in
// constant time, i.e. independent of the window size.
func (w *crc32Window) update(crc uint32, oldLeader, newTrailer byte) uint32 {
	// Inline crc32.simpleUpdate to compute crcExtended.
	t := ^crc
	t = crc32.IEEETable[byte(t)^newTrailer] ^ (t >> 8)
	crcExtended := ^t

	crcOldLeaderMasked := w.crcOldLeaderMaskedTable[oldLeader]

	// We'll show that
	//
	//   crc(a[1:n+1]) =
	//     crc(a[0:n+1]) ^ crc(a[0]..) ^ crc(ff000000ff..),
	//
	// where .. denotes padding with trailing 0s to have length n+1.
	//
	// First, let
	//
	//   crc(a) = ffffffff ^ crcz(ffffffff.. ^ a).
	//
	// From the identity
	//
	//   crcz(a ^ b) = crcz(a) ^ crcz(b)
	//
	// we can deduce that
	//
	//   crc(a ^ b ^ c) = crc(a) ^ crc(b) ^ crc(c).
	//
	// Furthermore, recall that
	//
	//   crcz(00.a) = crc(a),
	//
	// where . denotes concatenation.
	//
	// Then,
	//
	//   crc(a[1:n+1]) = ffffffff ^ crcz(a[1:n+1] ^ ffffffff..)
	//                 = ffffffff ^ crcz(00.a[1:n+1] ^ 00ffffffff..)
	//                 = ffffffff ^ crcz((a[0:n+1] ^ a[0]..) ^
	//                                   (ff000000ff.. ^ ffffffff..))
	//                 = crc(a[0:n+1] ^ a[0].. ^ ff000000ff..)
	//                 = crc(a[0:n+1]) ^ crc(a[0]..) ^ crc(ff000000ff..).
	//
	// Finally, we can precompute the latter two terms and build a
	// table indexed by a[0], which is precisely crcOldLeaderMasked.
	return crcExtended ^ crcOldLeaderMasked
}

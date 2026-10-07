// Vendored from github.com/akalin/gopar (see the file tail below for the
// original package), revision a776223763d3 (v0.0.0-20210524065929), as part
// of the streaming PAR2 implementation (plans/streaming_file.md §6).
// Original code:
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
// The full license text also lives in the LICENSE file next to this package.
// Adaptations against the original are limited to import paths and go.mod:
// see the diff of this file against the upstream revision.
package gf2p16

import (
	"reflect"
	"unsafe"

	"github.com/klauspost/cpuid/v2"
)

var hasSSSE3 bool

func init() {
	hasSSSE3 = cpuid.CPU.Supports(cpuid.SSSE3)
}

// MulByteSliceLE treats in and out as arrays of Ts stored in
// little-endian format, and sets each out<T>[i] to c.Times(in<T>[i]).
func MulByteSliceLE(c T, in, out []byte) {
	mulByteSliceLE(c, in, out, hasSSSE3)
}

func mulByteSliceLE(c T, in, out []byte, useSSSE3 bool) {
	if len(out) != len(in) {
		panic("size mismatch")
	}
	if len(in) == 0 {
		return
	}
	start := 0
	if useSSSE3 && len(in) >= 32 {
		mulSliceSSSE3Unsafe(&mulTable64[c], in, out)
		start = len(in) - (len(in) % 32)
		if start == len(in) {
			return
		}
	}
	mulByteSliceLEUnsafe(&mulTable[c], in[start:], out[start:])
}

// MulAndAddByteSliceLE treats in and out as arrays of Ts stored in
// little-endian format, and adds c.Times(in<T>[i]) to out<T>[i], for
// each i.
func MulAndAddByteSliceLE(c T, in, out []byte) {
	mulAndAddByteSliceLE(c, in, out, hasSSSE3)
}

func mulAndAddByteSliceLE(c T, in, out []byte, useSSSE3 bool) {
	if len(out) != len(in) {
		panic("size mismatch")
	}
	if len(in) == 0 {
		return
	}
	start := 0
	if useSSSE3 && len(in) >= 32 {
		mulAndAddSliceSSSE3Unsafe(&mulTable64[c], in, out)
		start = len(in) - (len(in) % 32)
		if start == len(in) {
			return
		}
	}
	mulAndAddByteSliceLEUnsafe(&mulTable[c], in[start:], out[start:])
}

func castTToByteSlice(ts []T) []byte {
	tsHdr := (*reflect.SliceHeader)(unsafe.Pointer(&ts))
	p := unsafe.Pointer(tsHdr.Data)

	var bs []byte
	bsHdr := (*reflect.SliceHeader)(unsafe.Pointer(&bs))
	bsHdr.Data = uintptr(p)
	bsHdr.Len = 2 * tsHdr.Len
	bsHdr.Cap = 2 * tsHdr.Cap
	return bs
}

func mulSlice(c T, in, out []T) {
	MulByteSliceLE(c, castTToByteSlice(in), castTToByteSlice(out))
}

func mulAndAddSlice(c T, in, out []T) {
	MulAndAddByteSliceLE(c, castTToByteSlice(in), castTToByteSlice(out))
}

//go:noescape
func mulByteSliceLEUnsafe(cEntry *mulTableEntry, in, out []byte)

//go:noescape
func mulAndAddByteSliceLEUnsafe(cEntry *mulTableEntry, in, out []byte)

// standardToAltMapSSSE3Unsafe sets
//
//	*outLow  = in0[0,2,4,6,8,10,12,14] . in1[0,2,4,6,8,10,12,14]
//	*outHigh = in0[1,3,5,7,9,11,13,15] . in1[1,3,5,7,9,11,13,15],
//
// where . denotes concatenation.
//
//go:noescape
func standardToAltMapSSSE3Unsafe(in0, in1, outLow, outHigh *[16]byte)

// standardToAltMapSliceSSSE3Unsafe behaves like calling
//
//	standardToAltMapSSSE3Unsafe(
//	  inChunk[16:32],  inChunk[0:16],
//	  outChunk[16:32], outChunk[0:16],
//	)
//
// on each subsequent pair of 32-byte chunks of in and out.
//
//go:noescape
func standardToAltMapSliceSSSE3Unsafe(in, out []byte)

// altToStandardMapSSSE3Unsafe is the inverse of
// standardToAltMapSSSE3Unsafe. That is, it sets
//
//	*out0 = inLow[0]  . inHigh[0]  . inLow[1]  . inHigh[1] . ...
//	      . inLow[6]  . inHigh[6]  . inLow[7]  . inHigh[7],
//	*out1 = inLow[8]  . inHigh[8]  . inLow[9]  . inHigh[9] . ...
//	      . inLow[14] . inHigh[14] . inLow[15] . inHigh[15].
//
//go:noescape
func altToStandardMapSSSE3Unsafe(inLow, inHigh, out0, out1 *[16]byte)

// altToStandardMapSliceSSSE3Unsafe behaves like calling
//
//	altToStandardMapSSSE3Unsafe(
//	  inChunk[16:32],  inChunk[0:16],
//	  outChunk[16:32], outChunk[0:16],
//	)
//
// on each subsequent pair of 32-byte chunks of in and out.
//
//go:noescape
func altToStandardMapSliceSSSE3Unsafe(in, out []byte)

// mulAltMapSSSE3Unsafe sets outLow and outHigh such that the
// following equation holds for each i:
//
//	(outHigh[i] << 8) | outLow[i] == c.Times((inHigh[i] << 8) | inLow[i]),
//
// where cEntry is &mulTable64[c].
//
//go:noescape
func mulAltMapSSSE3Unsafe(cEntry *mulTable64Entry, inLow, inHigh, outLow, outHigh *[16]byte)

// mulSliceAltMapSSSE3Unsafe behaves like calling
//
//	mulAltMapSSSE3Unsafe(
//	  cEntry,
//	  inChunk[16:32],  inChunk[0:16],
//	  outChunk[16:32], outChunk[0:16],
//	)
//
// on each subsequent pair of 32-byte chunks of in and out.
//
// go:noescape
func mulSliceAltMapSSSE3Unsafe(cEntry *mulTable64Entry, in, out []byte)

// mulSSSE3Unsafe sets out0 and out1 such that the following equations
// hold for each i:
//
//	out0[2*i] | (out0[2*i+1] << 8) == c.Times(in0[2*i] | in0[2*i+1] << 8)
//	out1[2*i] | (out1[2*i+1] << 8) == c.Times(in1[2*i] | in1[2*i+1] << 8),
//
// where cEntry is &mulTable64[c].
//
//go:noescape
func mulSSSE3Unsafe(cEntry *mulTable64Entry, in0, in1, out0, out1 *[16]byte)

// mulSliceAltMapSSSE3Unsafe behaves like calling
//
//	mulSliceSSSE3Unsafe(
//	  cEntry,
//	  inChunk[16:32],  inChunk[0:16],
//	  outChunk[16:32], outChunk[0:16],
//	)
//
// on each subsequent pair of 32-byte chunks of in and out.
//
// in and out must have the same length, which must be at least 32.
//
// go:noescape
func mulSliceSSSE3Unsafe(cEntry *mulTable64Entry, in, out []byte)

// mulAndAddSSSE3Unsafe is like mulSliceSSSE3Unsafe, except it adds
// (i.e., xors) to out0 and out1 instead of setting out0 and out1.
//
//go:noescape
func mulAndAddSSSE3Unsafe(cEntry *mulTable64Entry, in0, in1, out0, out1 *[16]byte)

// mulAndAddSliceAltMapSSSE3Unsafe behaves like calling
//
//	mulAndAddSliceSSSE3Unsafe(
//	  cEntry,
//	  inChunk[16:32],  inChunk[0:16],
//	  outChunk[16:32], outChunk[0:16],
//	)
//
// on each subsequent pair of 32-byte chunks of in and out.
//
// in and out must have the same length, which must be at least 32.
//
// go:noescape
func mulAndAddSliceSSSE3Unsafe(cEntry *mulTable64Entry, in, out []byte)

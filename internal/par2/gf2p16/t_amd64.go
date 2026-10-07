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

type mulTable64Entry struct {
	s0Low, s4Low, s8Low, s12Low     [1 << 4]byte
	s0High, s4High, s8High, s12High [1 << 4]byte
}

var mulTable64 [1 << 16]mulTable64Entry

func platformInit() {
	for i := 0; i < len(mulTable64); i++ {
		for j := 0; j < len(mulTable64[i].s0Low); j++ {
			t0 := T(i).Times(T(j))
			mulTable64[i].s0Low[j] = byte(t0)
			mulTable64[i].s0High[j] = byte(t0 >> 8)

			t1 := T(i).Times(T(j << 4))
			mulTable64[i].s4Low[j] = byte(t1)
			mulTable64[i].s4High[j] = byte(t1 >> 8)

			t2 := T(i).Times(T(j << 8))
			mulTable64[i].s8Low[j] = byte(t2)
			mulTable64[i].s8High[j] = byte(t2 >> 8)

			t3 := T(i).Times(T(j << 12))
			mulTable64[i].s12Low[j] = byte(t3)
			mulTable64[i].s12High[j] = byte(t3 >> 8)
		}
	}
}

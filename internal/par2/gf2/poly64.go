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
package gf2

// A Poly64 is a polynomial over GF(2) mod x^64.
type Poly64 uint64

// Plus returns the sum of p and q as polynomials over GF(2), which is
// just the bitwise xor of the two.
func (p Poly64) Plus(q Poly64) Poly64 {
	return p ^ q
}

// Minus returns the difference of p and q as polynomials over GF(2),
// which is just the bitwise xor of the two.
func (p Poly64) Minus(q Poly64) Poly64 {
	return p ^ q
}

// Times returns the product of p and q as polynomials over GF(2), mod
// x^64.
func (p Poly64) Times(q Poly64) Poly64 {
	var prod Poly64
	for p != 0 && q != 0 {
		if q&1 != 0 {
			prod ^= p
		}
		q >>= 1
		p <<= 1
	}
	return prod
}

// ilog2(n) returns floor(log2(n)), assuming n > 0.
func ilog2(n uint64) uint {
	var r uint
	for {
		n >>= 1
		if n == 0 {
			break
		}
		r++
	}
	return r
}

// Div performs Euclidean division using p and p2 (if non-zero) and
// returns the resulting quotient and remainder. That is, it returns q
// and r such that q.Times(p2).Plus(r) == p and either r == 0 or
// floor(log2(r)) < floor(log2(p2)). It panics if p2 == 0.
func (p Poly64) Div(p2 Poly64) (q, r Poly64) {
	if p2 == 0 {
		panic("division by zero")
	}

	q = 0
	r = p
	log2p2 := ilog2(uint64(p2))
	for r != 0 {
		log2r := ilog2(uint64(r))
		if log2r < log2p2 {
			break
		}
		dlog2 := log2r - log2p2
		q ^= (1 << dlog2)
		r ^= (p2 << dlog2)
	}

	return q, r
}

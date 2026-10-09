package rsec16

// The reconstruction half must match gopar's ReconstructData byte-for-byte
// (plans/repair.md §5): the recovered shards of a damaged set are pinned
// against gopar's coder, and the subset accumulation against Accumulate.

import (
	"math/rand"
	"slices"
	"testing"

	"partui/internal/par2/gf2p16"

	goparrsec16 "github.com/akalin/gopar/rsec16"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconstructionMatrix_EqGoparReconstructData damages a random set of
// shards and compares the slices our matrix reconstructs with what gopar's
// ReconstructData fills in. Both sides use the first `damaged` parity rows,
// so a singular submatrix must fail both or neither.
func TestReconstructionMatrix_EqGoparReconstructData(t *testing.T) {
	for _, tc := range []struct {
		dataShards, parityShards, damaged int
		scattered                         bool // contiguous {0..n} missing when false
	}{
		{1, 3, 1, false},
		{2, 3, 1, true},
		{5, 3, 2, false},
		{5, 3, 2, true},
		{17, 5, 3, true},
		{33, 4, 4, true},
	} {
		t.Run("", func(t *testing.T) {
			runReconstructCase(t, tc.dataShards, tc.parityShards, tc.damaged, tc.scattered)
		})
	}
}

func runReconstructCase(t *testing.T, dataShards, parityShards, damaged int, scattered bool) {
	t.Helper()
	const shardBytes = 64
	rng := rand.New(rand.NewSource(int64(dataShards*100 + damaged)))

	pm, err := NewPAR2Vandermonde(dataShards, parityShards, 1)
	require.NoError(t, err)

	data := make([][]byte, dataShards)
	for i := range data {
		data[i] = make([]byte, shardBytes)
		rng.Read(data[i])
	}
	parity := make([][]byte, parityShards)
	for i := range parity {
		parity[i] = make([]byte, shardBytes)
	}
	for j := range data {
		pm.Accumulate(parity, j, data[j])
	}

	// the damaged slices: contiguous from the front (always invertible —
	// the submatrix is a Vandermonde) or a scattered random choice
	var missing []int
	if scattered {
		missing = rng.Perm(dataShards)[:damaged]
		slices.Sort(missing)
	} else {
		for i := range damaged {
			missing = append(missing, i)
		}
	}
	usedParity := make([]int, damaged) // gopar order: the first rows
	for i := range usedParity {
		usedParity[i] = i
	}

	// gopar's answer
	coder, err := goparrsec16.NewCoderPAR2Vandermonde(dataShards, parityShards, 1)
	require.NoError(t, err)
	gData := make([][]byte, dataShards)
	for i := range gData {
		gData[i] = append([]byte(nil), data[i]...)
	}
	for _, idx := range missing {
		gData[idx] = nil
	}
	gParity := make([][]byte, parityShards)
	for i := range gParity {
		gParity[i] = append([]byte(nil), parity[i]...)
	}
	gErr := coder.ReconstructData(gData, gParity)

	// ours: syndromes first, then the recovered slices
	syndromes := make([][]byte, damaged)
	for k := range syndromes {
		syndromes[k] = append([]byte(nil), parity[usedParity[k]]...)
	}
	missingSet := make(map[int]bool, damaged)
	for _, idx := range missing {
		missingSet[idx] = true
	}
	for j := range data {
		if missingSet[j] {
			continue
		}
		pm.AccumulateSelected(usedParity, syndromes, j, data[j])
	}

	inv, err := pm.ReconstructionMatrix(missing, usedParity)
	if gErr != nil {
		require.Error(t, err, "gopar failed (%v), the same submatrix must fail here", gErr)
		return
	}
	require.NoError(t, err)

	for j := range syndromes {
		rec := make([]byte, shardBytes)
		for k := range syndromes {
			gf2p16.MulAndAddByteSliceLE(inv.At(j, k), syndromes[k], rec)
		}
		assert.Equal(t, gData[missing[j]], rec, "recovered slice %d (shard %d) differs from gopar's", j, missing[j])
	}
}

// TestAccumulateSelected_EqAccumulate pins the subset accumulation against
// the plain one: the selected rows of Accumulate must be AccumulateSelected.
func TestAccumulateSelected_EqAccumulate(t *testing.T) {
	pm, err := NewPAR2Vandermonde(9, 4, 1)
	require.NoError(t, err)

	src := make([]byte, 300)
	rand.New(rand.NewSource(42)).Read(src)

	all := make([][]byte, 4)
	for i := range all {
		all[i] = make([]byte, len(src))
	}
	pm.Accumulate(all, 3, src)

	rows := []int{3, 0}
	sel := make([][]byte, 2)
	for i := range sel {
		sel[i] = make([]byte, len(src))
	}
	pm.AccumulateSelected(rows, sel, 3, src)

	for k, row := range rows {
		assert.Equal(t, all[row], sel[k], "row %d differs", row)
	}
}

// TestReconstructionMatrix_Errors pins the argument checks and the singular
// path — the known flaw of the PAR2 Vandermonde construction.
func TestReconstructionMatrix_Errors(t *testing.T) {
	pm, err := NewPAR2Vandermonde(4, 2, 1)
	require.NoError(t, err)

	_, err = pm.ReconstructionMatrix(nil, nil)
	assert.Error(t, err, "no missing rows must be an error")

	_, err = pm.ReconstructionMatrix([]int{0, 1}, []int{0})
	assert.Error(t, err, "mismatched row counts must be an error")

	singular := ParityMatrix{matrix: gf2p16.NewZeroMatrix(2, 2), numGoroutines: 1}
	_, err = singular.ReconstructionMatrix([]int{0, 1}, []int{0, 1})
	assert.Error(t, err, "a singular submatrix must be an error")
}

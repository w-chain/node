package precompiled

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/contracts"
	"github.com/w-chain-team/node/helper/hex"
	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

// PS-M1: from WChainV110 modexp is priced as in EIP-7883. The vectors are
// go-ethereum's core/vm/testdata/precompiles/modexp_eip7883.json.
func TestModExpGas_EIP7883(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("fixtures/modexp_eip7883.json")
	require.NoError(t, err)

	var vectors []struct {
		Input string
		Gas   uint64
		Name  string
	}

	require.NoError(t, json.Unmarshal(raw, &vectors))
	require.NotEmpty(t, vectors)

	m := &modExp{p: &Precompiled{}}
	v110 := &chain.ForksInTime{Byzantium: true, WChainV109: true, WChainV110: true}

	for _, v := range vectors {
		in, err := hex.DecodeHex(v.Input)
		require.NoError(t, err, v.Name)
		require.Equal(t, v.Gas, m.gas(in, v110), v.Name)
	}
}

// PS-M1: the audit's shape — 1-byte modulus, long zero exponent — before and
// after WChainV110.
func TestModExpGas_V110AttackShape(t *testing.T) {
	t.Parallel()

	m := &modExp{p: &Precompiled{}}
	in := modExpInput([]byte{3}, make([]byte, 1000), []byte{7})

	v109 := m.gas(in, &chain.ForksInTime{Byzantium: true, WChainV109: true})
	v110 := m.gas(in, &chain.ForksInTime{Byzantium: true, WChainV109: true, WChainV110: true})

	require.Equal(t, uint64(2581), v109)
	require.Equal(t, uint64(16*(1000-32)*16), v110)
}

// PS-C1: from WChainV110 the PolyBFT-only precompiles do not run; the
// addresses behave as empty accounts.
func TestPolyBFTPrecompilesOffFromV110(t *testing.T) {
	t.Parallel()

	p := NewPrecompiled()

	for _, addr := range []types.Address{
		contracts.NativeTransferPrecompile,
		contracts.BLSAggSigsVerificationPrecompile,
	} {
		c := &runtime.Contract{CodeAddress: addr}

		require.True(t, p.CanRun(c, nil, &chain.ForksInTime{WChainV109: true}), addr)
		require.False(t, p.CanRun(c, nil, &chain.ForksInTime{WChainV109: true, WChainV110: true}), addr)
	}

	// The Ethereum precompiles are unaffected.
	ecrecover := &runtime.Contract{CodeAddress: types.StringToAddress("1")}
	require.True(t, p.CanRun(ecrecover, nil, &chain.ForksInTime{WChainV110: true}))
}

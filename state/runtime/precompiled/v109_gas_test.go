package precompiled

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
)

func modExpInput(base, exp, mod []byte) []byte {
	word := func(n int) []byte { return common32(big.NewInt(int64(n)).Bytes()) }

	in := append(append(append([]byte{}, word(len(base))...), word(len(exp))...), word(len(mod))...)

	return append(append(append(in, base...), exp...), mod...)
}

func common32(b []byte) []byte {
	return append(make([]byte, 32-len(b)), b...)
}

// E-H1: from WChainV109 modexp is priced as in EIP-2565. Expected values are
// the gas figures of the EIP-2565 / geth reference vectors.
func TestModExpGas_EIP2565(t *testing.T) {
	t.Parallel()

	m := &modExp{p: &Precompiled{}}
	v109 := &chain.ForksInTime{Byzantium: true, WChainV109: true}

	ones := func(n int) []byte { return bytes.Repeat([]byte{0xff}, n) }

	cases := []struct {
		name string
		in   []byte
		gas  uint64
	}{
		{"nagydani-1-square", modExpInput(ones(64), []byte{2}, ones(64)), 200},
		{"nagydani-1-pow0x10001", modExpInput(ones(64), []byte{1, 0, 1}, ones(64)), 341},
		{"nagydani-2-square", modExpInput(ones(128), []byte{2}, ones(128)), 200},
		{"nagydani-2-pow0x10001", modExpInput(ones(128), []byte{1, 0, 1}, ones(128)), 1365},
		{"nagydani-3-square", modExpInput(ones(256), []byte{2}, ones(256)), 341},
		{"nagydani-3-pow0x10001", modExpInput(ones(256), []byte{1, 0, 1}, ones(256)), 5461},
		{"nagydani-4-pow0x10001", modExpInput(ones(512), []byte{1, 0, 1}, ones(512)), 21845},
		{"nagydani-5-square", modExpInput(ones(1024), []byte{2}, ones(1024)), 5461},
		{"nagydani-5-pow0x10001", modExpInput(ones(1024), []byte{1, 0, 1}, ones(1024)), 87381},
		{"eip_example1", modExpInput([]byte{3}, ones(32), ones(32)), 1360},
	}

	for _, c := range cases {
		require.Equal(t, c.gas, m.gas(c.in, v109), c.name)
	}
}

// E-H1: the attack shape (1-byte modulus, long declared exponent) got ~6.7x
// more expensive; before the fork the old price is unchanged.
func TestModExpGas_ForkBoundary(t *testing.T) {
	t.Parallel()

	m := &modExp{p: &Precompiled{}}

	// 1-byte base and modulus, 100 KB declared exponent that is not in the input.
	in := append(append(common32([]byte{1}), common32(big.NewInt(100_000).Bytes())...), common32([]byte{1})...)

	before := m.gas(in, &chain.ForksInTime{Byzantium: true})
	after := m.gas(in, &chain.ForksInTime{Byzantium: true, WChainV109: true})

	require.Equal(t, uint64(39_987), before, "EIP-198 price stays the same before the fork")
	require.Equal(t, uint64(266_581), after)
}

// E-C2: the BLS precompile is priced by input size from WChainV109.
func TestBLSAggSigsGas_ForkBoundary(t *testing.T) {
	t.Parallel()

	c := &blsAggSignsVerification{}
	in := make([]byte, 127_104) // 660 public keys

	require.Equal(t, uint64(150_000), c.gas(in, &chain.ForksInTime{}), "flat before the fork")
	require.Equal(t, uint64(150_000+1000*3972), c.gas(in, &chain.ForksInTime{WChainV109: true}))
	require.Equal(t, uint64(150_000), c.gas(nil, &chain.ForksInTime{WChainV109: true}))
}

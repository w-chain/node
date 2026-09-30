package ibft

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/consensus"
	"github.com/w-chain-team/node/types"
)

func TestIsWChainV109(t *testing.T) {
	t.Parallel()

	require.False(t, (&backendIBFT{}).isWChainV109(1))

	// Only WChainV108 in genesis: v109 stays dormant.
	require.False(t, (&backendIBFT{config: newV108Config(0)}).isWChainV109(1_000_000))

	i := &backendIBFT{config: &consensus.Config{
		Params: &chain.Params{Forks: &chain.Forks{chain.WChainV109: chain.NewFork(100)}},
	}}
	require.False(t, i.isWChainV109(99))
	require.True(t, i.isWChainV109(100))
}

func TestVerifyV109Header(t *testing.T) {
	t.Parallel()

	require.NoError(t, verifyV109Header(&types.Header{Timestamp: 1_759_000_000}))
	require.NoError(t, verifyV109Header(&types.Header{Timestamp: math.MaxInt64}))

	require.ErrorIs(t, verifyV109Header(&types.Header{Timestamp: math.MaxInt64 + 1}), errTimestampOutOfRange)
	require.ErrorIs(t, verifyV109Header(&types.Header{Timestamp: math.MaxUint64}), errTimestampOutOfRange)
}

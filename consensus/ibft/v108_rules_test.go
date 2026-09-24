package ibft

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/consensus"
	"github.com/w-chain-team/node/types"
)

func TestIsWChainV108(t *testing.T) {
	t.Parallel()

	// Absent from the genesis: never active, which is the state of every
	// existing chain until the fork block is scheduled.
	require.False(t, (&backendIBFT{}).isWChainV108(1))
	require.False(t, (&backendIBFT{config: &consensus.Config{
		Params: &chain.Params{Forks: &chain.Forks{chain.London: chain.NewFork(0)}},
	}}).isWChainV108(1))

	i := &backendIBFT{config: newV108Config(100)}
	require.False(t, i.isWChainV108(99))
	require.True(t, i.isWChainV108(100))
	require.True(t, i.isWChainV108(101))
}

func TestVerifyV108Header(t *testing.T) {
	t.Parallel()

	parent := &types.Header{Number: 9, Timestamp: 1000}

	require.NoError(t, verifyV108Header(parent, &types.Header{Timestamp: 1002, BaseFee: 800}, 800))

	// H4: equal or earlier timestamps.
	require.ErrorIs(t, verifyV108Header(parent, &types.Header{Timestamp: 1000, BaseFee: 800}, 800),
		errTimestampNotAfterParent)
	require.ErrorIs(t, verifyV108Header(parent, &types.Header{Timestamp: 999, BaseFee: 800}, 800),
		errTimestampNotAfterParent)

	// M1: a proposer choosing its own base fee, too low or too high.
	require.ErrorIs(t, verifyV108Header(parent, &types.Header{Timestamp: 1002, BaseFee: 0}, 800),
		errInvalidBaseFee)
	require.ErrorIs(t, verifyV108Header(parent, &types.Header{Timestamp: 1002, BaseFee: 801}, 800),
		errInvalidBaseFee)
}

func TestVerifyV108Proposal(t *testing.T) {
	t.Parallel()

	now := time.Unix(10_000, 0)
	block := func(ts uint64, txs ...*types.Transaction) *types.Block {
		return &types.Block{Header: &types.Header{Timestamp: ts}, Transactions: txs}
	}

	legacy := &types.Transaction{Type: types.LegacyTx}
	dynamic := &types.Transaction{Type: types.DynamicFeeTx}
	state := &types.Transaction{Type: types.StateTx}

	// An honest proposer is at most one block time ahead of the voter.
	require.NoError(t, verifyV108Proposal(block(10_002, legacy, dynamic), now))
	require.NoError(t, verifyV108Proposal(block(10_000+uint64(maxFutureBlockTime.Seconds())), now))

	// H4: far-future timestamps stall the next proposers.
	require.ErrorIs(t, verifyV108Proposal(block(10_000+uint64(maxFutureBlockTime.Seconds())+1), now),
		errTimestampTooFarAhead)

	// M2: system state transactions are never valid in IBFT blocks.
	require.ErrorIs(t, verifyV108Proposal(block(10_002, legacy, state), now), errStateTxNotAllowed)
}

func newV108Config(block uint64) *consensus.Config {
	return &consensus.Config{
		Params: &chain.Params{Forks: &chain.Forks{chain.WChainV108: chain.NewFork(block)}},
	}
}

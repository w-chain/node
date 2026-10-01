package gasprice

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/helper/tests"
	"github.com/w-chain-team/node/types"
)

// RPC-L2: one tx with a 1000 gwei tip among 30 otherwise empty blocks used to
// be sampled again and again by the "collect more" loop, pinning the
// suggested tip at the 1000 gwei cap.
func TestMaxPriorityFee_OneHighTipTxDoesNotDominate(t *testing.T) {
	backend := createTestBlocks(t, 30)
	forks := chain.AllForksEnabled.Copy()
	forks.SetFork(chain.WChainV108, chain.NewFork(0))
	backend.forks = forks

	key, sender := tests.GenerateKeyAndAddr(t)
	signer := crypto.NewSigner(backend.Config().Forks.At(10), uint64(backend.Config().ChainID))
	tx, err := signer.SignTx(&types.Transaction{
		From: sender, Value: big.NewInt(0), To: &types.ZeroAddress, Type: types.DynamicFeeTx,
		GasTipCap: ethgo.Gwei(1000), GasFeeCap: ethgo.Gwei(5000),
	}, key)
	require.NoError(t, err)

	backend.blocksByNumber[10].Transactions = []*types.Transaction{tx}

	gh, err := NewGasHelper(DefaultGasHelperConfig, backend)
	require.NoError(t, err)

	price, err := gh.MaxPriorityFeePerGas()
	require.NoError(t, err)
	require.Less(t, price.Cmp(ethgo.Gwei(1000)), 0, "a single 1000 gwei tip must not set the suggestion: got %s", price)
}

// RPC-L1: percentiles were walked with gas*price (wei) against a threshold
// in gas, so every percentile returned the lowest tip.
func TestFeeHistory_RewardPercentiles(t *testing.T) {
	backend := createTestBlocks(t, 2)
	backend.forks = chain.AllForksEnabled.Copy()

	b := backend.blocksByNumber[2]
	b.Header.BaseFee = 800_000_000_000
	b.Header.GasUsed = 210_000
	b.Transactions = nil

	for j := 0; j < 10; j++ {
		b.Transactions = append(b.Transactions, &types.Transaction{
			Type: types.DynamicFeeTx, Gas: 21000, Value: big.NewInt(0),
			GasTipCap: big.NewInt(int64(j+1) * 1_000_000_000),
			GasFeeCap: big.NewInt(10_000_000_000_000),
		})
	}

	gh, err := NewGasHelper(DefaultGasHelperConfig, backend)
	require.NoError(t, err)

	res, err := gh.FeeHistory(1, 2, []float64{10, 50, 90, 100})
	require.NoError(t, err)
	require.Len(t, res.Reward, 1)
	require.Equal(t, []uint64{1e9, 5e9, 9e9, 10e9}, res.Reward[0])
}

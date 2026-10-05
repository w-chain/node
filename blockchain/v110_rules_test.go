package blockchain

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
	"github.com/w-chain-team/node/types/buildroot"
)

// BI-H1: on mainnet WChainV108 and WChainV110 start at the same block X. The
// base fee of X-1 is neither checked nor hashed, so a peer can change it; X
// must not depend on it.
func TestCalculateBaseFee_ForkBlockAnchor(t *testing.T) {
	t.Parallel()

	const x = 100

	chainWith := func(v110 bool) *Blockchain {
		forks := chain.AllForksEnabled.Copy()
		(*forks)[chain.WChainV108] = chain.NewFork(x)
		(*forks)[chain.WChainV109] = chain.NewFork(x)
		forks.RemoveFork(chain.WChainV110)

		if v110 {
			(*forks)[chain.WChainV110] = chain.NewFork(x)
		}

		return &Blockchain{config: &chain.Chain{
			Params:  &chain.Params{Forks: forks},
			Genesis: &chain.Genesis{BaseFee: 7_480_000_000, BaseFeeEM: 2, BaseFeeChangeDenom: 10},
		}}
	}

	tampered := &types.Header{Number: x - 1, GasLimit: 20_000_000, BaseFee: 5_000 * chain.MinGasPrice}

	require.Equal(t, chain.MinGasPrice, chainWith(true).CalculateBaseFee(tampered),
		"first fork block ignores the parent's base fee")
	require.NotEqual(t, chain.MinGasPrice, chainWith(false).CalculateBaseFee(tampered),
		"without WChainV110 at the same block the old rule applies (testnet: v108 already active)")

	// After the fork block the parent's (now verified) base fee is used again.
	next := &types.Header{Number: x, GasLimit: 20_000_000, BaseFee: chain.MinGasPrice}
	require.Equal(t, chain.MinGasPrice, chainWith(true).CalculateBaseFee(next))
}

// BI-1: from WChainV110 a block's header must carry the logs bloom of its
// receipts.
func TestVerifyBlock_LogsBloom(t *testing.T) {
	t.Parallel()

	// LOG0(0, 0)
	logger := types.StringToAddress("0x10660000000000000000000000000000000000aa")
	alloc := func() map[types.Address]*chain.GenesisAccount {
		return map[types.Address]*chain.GenesisAccount{
			plSender: {Balance: new(big.Int).Lsh(big.NewInt(1), 100)},
			logger:   {Code: []byte{0x60, 0x00, 0x60, 0x00, 0xa0, 0x00}},
		}
	}

	for _, v110 := range []bool{false, true} {
		params := plParams()
		params.Forks = chain.AllForksEnabled.Copy()

		if !v110 {
			params.Forks.RemoveFork(chain.WChainV110)
		}

		prodEx := state.NewExecutor(params, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
		prodEx.GetHash = plNoHash
		root0, err := prodEx.WriteGenesis(alloc(), types.ZeroHash)
		require.NoError(t, err)

		genesis := &chain.Genesis{GasLimit: 20_000_000, StateRoot: root0, Alloc: alloc()}
		g := genesis.GenesisHeader()
		g.ComputeHash()

		header := &types.Header{
			ParentHash: g.Hash, Number: 1, GasLimit: g.GasLimit, BaseFee: chain.MinGasPrice, Difficulty: 1,
			Timestamp: 2, Sha3Uncles: types.EmptyUncleHash, Miner: plCoinbase.Bytes(),
		}
		tx := &types.Transaction{
			From: plSender, To: &logger, Gas: 50_000, GasPrice: new(big.Int).SetUint64(chain.MinGasPrice),
			Value: big.NewInt(0),
		}
		tx.ComputeHash(1)

		txn, err := prodEx.BeginTxn(g.StateRoot, header, plCoinbase)
		require.NoError(t, err)
		require.NoError(t, txn.Write(tx))

		_, root, err := txn.Commit()
		require.NoError(t, err)

		header.StateRoot = root
		header.GasUsed = txn.TotalGas()
		header.ReceiptsRoot = buildroot.CalculateReceiptsRoot(txn.Receipts())
		header.TxRoot = buildroot.CalculateTransactionsRoot([]*types.Transaction{tx}, 1)
		// header.LogsBloom left empty, as every block before WChainV110
		header.ComputeHash()

		block := &types.Block{Header: header, Transactions: []*types.Transaction{tx}}

		n := openWith(t, t.TempDir(), genesis, params, alloc())
		_, err = n.b.VerifyFinalizedBlock(block)

		if v110 {
			require.ErrorIs(t, err, ErrInvalidLogsBloom)

			// BI-H1 (no fork): receipts of a block that failed verification
			// are not cached.
			_, cached := n.b.receiptsCache.Get(block.Header.Hash)
			require.False(t, cached, "receipts of an invalid block were cached")

			goodHeader := header.Copy()
			goodHeader.LogsBloom = types.CreateBloom(txn.Receipts())
			goodHeader.ComputeHash()
			_, err = n.b.VerifyFinalizedBlock(&types.Block{Header: goodHeader, Transactions: block.Transactions})
			require.NoError(t, err)
		} else {
			require.NoError(t, err, "old rule kept before the fork")
		}

		n.close()
	}
}

func openWith(t *testing.T, dir string, genesis *chain.Genesis, params *chain.Params, alloc map[types.Address]*chain.GenesisAccount) *plNode {
	t.Helper()

	n := plOpenParams(t, dir, genesis, params)
	_, err := n.ex.WriteGenesis(alloc, types.ZeroHash)
	require.NoError(t, err)
	require.NoError(t, n.b.ComputeGenesis())

	return n
}

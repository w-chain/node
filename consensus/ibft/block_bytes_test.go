package ibft

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/consensus/ibft/hook"
	"github.com/w-chain-team/node/network"
	"github.com/w-chain-team/node/txpool"
	"github.com/w-chain-team/node/types"
)

// NET-C1: the budget keeps a block, and the (1+2N)x message after one failed
// prepared round, under the gossip limit for the live validator count.
func TestBlockByteBudget(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 1, 4, 6, 12, 20, 40, 49, 50, 63, 200} {
		budget := blockByteBudget(n)

		require.LessOrEqual(t, budget, uint64(maxBlockBytes), "n=%d: never above the hard cap", n)
		require.GreaterOrEqual(t, budget, uint64(minBlockBytes), "n=%d: never below the floor", n)
	}

	// Up to maxGuaranteedValidators the (1+2N) amplification of one failed
	// prepared round stays under the gossip limit.
	for n := 1; n <= maxGuaranteedValidators; n++ {
		worst := blockByteBudget(n)*uint64(1+2*n) + blockBytesMargin
		require.LessOrEqual(t, worst, uint64(network.MaxGossipMessageSize),
			"n=%d: one failed prepared round must still fit the gossip limit", n)
	}

	require.Equal(t, uint64(maxBlockBytes), blockByteBudget(4), "small sets hit the hard cap")
	require.Equal(t, uint64(minBlockBytes), blockByteBudget(500), "huge sets hit the floor")
	require.Less(t, blockByteBudget(12), blockByteBudget(6), "more validators, smaller blocks")
}

// NET-C1: block building stops at the byte budget, skipping a transaction
// that does not fit the space left while smaller ones from other accounts
// still go in. The skipped ones wait for the next block.
func TestWriteTransactions_ByteBudget(t *testing.T) {
	const (
		blockGas = 20_000_000
		budget   = 300 << 10 // 300 KB for transactions
		bigTx    = 100 << 10 // ~100 KB each
	)

	forks := chain.AllForksEnabled.Copy()
	forks.RemoveFork(chain.London)

	pool, err := txpool.NewTxPool(hclog.NewNullLogger(), forks,
		starveStore{&types.Header{GasLimit: blockGas}}, nil, nil,
		&txpool.Config{PriceLimit: 1, MaxSlots: 4096, MaxAccountEnqueued: 128})
	require.NoError(t, err)
	pool.SetSigner(starveSigner{})
	pool.Start()
	defer pool.Close()

	mk := func(from types.Address, inputLen int, price uint64) *types.Transaction {
		to := types.Address{0x99}

		return &types.Transaction{From: from, To: &to, Gas: 1_000_000,
			GasPrice: new(big.Int).SetUint64(price), Value: big.NewInt(0),
			Input: append(make([]byte, inputLen), from.Bytes()...)}
	}

	// 5 big txs from 5 accounts at the top price: 500 KB, only 2 fit in 300 KB.
	for i := 0; i < 5; i++ {
		require.NoError(t, pool.AddTx(mk(types.Address{0xaa, byte(i)}, bigTx, 3000)))
	}

	// 50 small transfers at a lower price must still get in.
	for i := 0; i < 50; i++ {
		require.NoError(t, pool.AddTx(mk(types.Address{0x10, byte(i)}, 0, 1000)))
	}

	require.Eventually(t, func() bool { return pool.Length() == 55 }, 3*time.Second, 10*time.Millisecond)

	b := &backendIBFT{logger: hclog.NewNullLogger(), txpool: pool}
	b.currentModules.Store(&ibftModules{hooks: &hook.Hooks{}})

	tr := &starveTransition{pool: blockGas}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	executed := b.writeTransactions(ctx, blockGas, budget, 1, tr)

	var bigCount, bytes uint64

	for _, tx := range executed {
		bytes += tx.Size() + txBytesFraming

		if len(tx.Input) > bigTx {
			bigCount++
		}
	}

	require.Equal(t, uint64(2), bigCount, "two 100 KB txs fit in 300 KB")
	require.Len(t, executed, 52, "2 big + 50 small")
	require.LessOrEqual(t, bytes, uint64(budget))

	// The 3 big txs that did not fit are skipped, not dropped: they wait.
	require.Eventually(t, func() bool { return pool.Length() == 3 }, 3*time.Second, 10*time.Millisecond)
}

// NET-C1: a transaction larger than the whole budget can never fit any block,
// so it is dropped rather than skipped forever. In production the budget floor
// (160 KB) is above the 128 KB pool limit, so this is a defensive guard; it is
// exercised here with a deliberately tiny budget.
func TestWriteTransaction_DropsTxLargerThanBudget(t *testing.T) {
	b := &backendIBFT{logger: hclog.NewNullLogger()}

	dropped := false
	b.txpool = &dropRecorder{onDrop: func() { dropped = true }}

	to := types.Address{0x99}
	tx := &types.Transaction{From: types.Address{0x01}, To: &to, Gas: 1_000_000,
		GasPrice: big.NewInt(1), Value: big.NewInt(0), Input: make([]byte, 100<<10)}

	res, ok := b.writeTransaction(tx, &starveTransition{pool: 20_000_000}, 20_000_000, 50<<10, 50<<10)

	require.True(t, ok, "processing continues after the drop")
	require.Equal(t, fail, res.status)
	require.True(t, dropped, "a tx bigger than any block must be dropped")
}

// dropRecorder is a txpool stand-in for the drop-branch test.
type dropRecorder struct{ onDrop func() }

func (d *dropRecorder) Drop(*types.Transaction)   { d.onDrop() }
func (d *dropRecorder) Demote(*types.Transaction) {}
func (d *dropRecorder) Pop(*types.Transaction)    {}
func (d *dropRecorder) Peek() *types.Transaction  { return nil }
func (d *dropRecorder) Prepare()                  {}
func (d *dropRecorder) Length() uint64            { return 0 }

func (d *dropRecorder) ResetWithHeaders(...*types.Header) {}

func (d *dropRecorder) SetSealing(bool) {}

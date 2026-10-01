package txpool

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/types"
)

// popTestStore is a pointer-based store mock so the test can mutate the reported
// account nonce / returned block between steps (defaultMockStore is passed by
// value, so its fields cannot be changed after NewTxPool copies it).
type popTestStore struct {
	header  *types.Header
	nonce   uint64
	block   *types.Block
	balance *big.Int
}

func (m *popTestStore) Header() *types.Header                     { return m.header }
func (m *popTestStore) GetNonce(types.Hash, types.Address) uint64 { return m.nonce }
func (m *popTestStore) GetBalance(types.Hash, types.Address) (*big.Int, error) {
	return m.balance, nil
}
func (m *popTestStore) GetBlockByHash(types.Hash, bool) (*types.Block, bool) {
	if m.block == nil {
		return nil, false
	}
	return m.block, true
}
func (m *popTestStore) CalculateBaseFee(*types.Header) uint64 { return 0 }

func pocForks() *chain.Forks {
	return &chain.Forks{
		chain.Homestead: chain.NewFork(0),
		chain.Istanbul:  chain.NewFork(0),
		chain.London:    chain.NewFork(0),
	}
}

// TestPoC_PopAfterResetUnderflowsGauge demonstrates that TxPool.Pop
// unconditionally decreases the slot gauge and the pending counter, even when
// the promoted transaction it is asked to pop has already been removed from the
// account (e.g. because the block that mined it was inserted by the syncer
// goroutine while the consensus goroutine was mid-build).
//
// Real reachability: consensus (startConsensus) runs Prepare/Peek/Pop in one
// goroutine; the syncer (go startSyncing -> ResetWithHeaders) prunes promoted
// txs in another. This test invokes the exact operation order that race
// produces, so it is deterministic.
func TestPop_AfterResetDoesNotUnderflowGauge(t *testing.T) {
	addr := types.Address{0x1}
	hdr := &types.Header{Number: 0, GasLimit: 1_000_000_000, BaseFee: 0}
	store := &popTestStore{header: hdr, nonce: 0, balance: big.NewInt(1_000_000_000_000_000)}

	pool, err := NewTxPool(
		hclog.NewNullLogger(),
		pocForks(),
		store,
		nil,
		nil,
		&Config{PriceLimit: 1, MaxSlots: 4096, MaxAccountEnqueued: 128, ChainID: big.NewInt(100)},
	)
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	// a single legacy tx with the account's expected nonce (0)
	tx := newTx(addr, 0, 1)
	require.NoError(t, pool.addTx(local, tx))

	// drive the (otherwise async) promotion deterministically
	pool.handlePromoteRequest(<-pool.promoteReqCh)

	usedBefore, _ := pool.GetCapacity()
	require.Equal(t, uint64(1), usedBefore, "one slot should be occupied")
	require.Equal(t, int64(1), pool.pending, "one pending tx")

	// --- consensus goroutine: build a block, peek the primary ---
	pool.Prepare()
	peeked := pool.Peek()
	require.NotNil(t, peeked)
	require.Equal(t, tx.Hash, peeked.Hash)

	// --- syncer goroutine: the block that mines `tx` gets inserted ---
	// account's on-chain nonce is now 1, and the block contains the tx.
	store.nonce = 1
	store.block = &types.Block{
		Header:       &types.Header{Number: 1, Hash: types.Hash{0xbb}},
		Transactions: []*types.Transaction{tx},
	}
	pool.ResetWithHeaders(&types.Header{Number: 1, Hash: types.Hash{0xbb}})

	usedAfterReset, _ := pool.GetCapacity()
	require.Equal(t, uint64(0), usedAfterReset, "reset should have freed the only slot")

	// --- consensus goroutine finishes: it still Pop()s the tx it peeked ---
	pool.Pop(peeked)

	usedFinal, max := pool.GetCapacity()
	free := pool.gauge.freeSlots()

	t.Logf("gauge height after stale Pop = %d (max=%d)", usedFinal, max)
	t.Logf("gauge.freeSlots() = %d", free)
	t.Logf("pool.pending = %d", pool.pending)
	t.Logf("gauge.highPressure() = %v", pool.gauge.highPressure())

	// The reset already removed the tx, so the stale Pop must change nothing:
	// before, it decremented again and the gauge wrapped to ~2^64, leaving an
	// empty pool reporting itself full until a restart.
	require.Equal(t, uint64(0), usedFinal)
	require.Equal(t, max, free)
	require.Equal(t, int64(0), pool.pending)
	require.False(t, pool.gauge.highPressure())
}

// TP-H1: the gauge stops at zero instead of wrapping to ~2^64, which left the
// pool reporting itself full on every tx until a restart.
func TestSlotGauge_DecreaseSaturates(t *testing.T) {
	t.Parallel()

	g := slotGauge{height: 3, max: 4096}
	g.decrease(5)
	require.Equal(t, uint64(0), g.read())

	g.increase(2)
	g.decrease(1)
	require.Equal(t, uint64(1), g.read())
}

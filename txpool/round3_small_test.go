package txpool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

// TP-L1: once enqueued was pushed past the limit (a nonce==next tx is queued
// before its async promotion runs), the == check never matched again and one
// account could queue without limit.
func TestEnqueuedLimit_HoldsAfterOvershoot(t *testing.T) {
	pool, err := newTestPool() // not started: promotion has not run yet
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	for n := uint64(2); n < 2+defaultMaxAccountEnqueued; n++ {
		require.NoError(t, pool.addTx(local, newTx(addr1, n, 1)))
	}

	// The expected nonce is always allowed, which takes the queue past the limit.
	require.NoError(t, pool.addTx(local, newTx(addr1, 0, 1)))

	for n := uint64(1000); n < 1300; n++ {
		require.ErrorIs(t, pool.addTx(local, newTx(addr1, n, 1)), ErrMaxEnqueuedLimitReached)
	}

	require.LessOrEqual(t, pool.accounts.get(addr1).enqueued.length(), defaultMaxAccountEnqueued+1)
}

// TP-L2: a popped tx left the pool but stayed in the hash index, so it was
// still "known" and "pending" forever if its block was never committed.
func TestPop_RemovesFromIndex(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	tx := newTx(addr1, 0, 1)
	require.NoError(t, pool.addTx(local, tx))
	pool.handlePromoteRequest(<-pool.promoteReqCh)

	pool.Prepare()
	peeked := pool.Peek()
	require.Equal(t, tx.Hash, peeked.Hash)

	pool.Pop(peeked)

	_, known := pool.index.get(tx.Hash)
	require.False(t, known, "a popped tx must leave the hash index")
}

// TP-L3: allTxs handed out the live queue slices, read after the lock was
// released while promotion and Pop kept changing them.
func TestAllTxs_ReturnsCopies(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	for n := uint64(0); n < 3; n++ {
		require.NoError(t, pool.addTx(local, newTx(addr1, n, 1)))
		pool.handlePromoteRequest(<-pool.promoteReqCh)
	}

	promoted, _ := pool.accounts.allTxs(false)
	require.Len(t, promoted[addr1], 3)

	promoted[addr1][0] = nil // must not reach the pool's own queue

	require.NotNil(t, pool.accounts.get(addr1).promoted.peek())
}

// TX-L1: the replacement-price check read baseFee without the atomic that
// SetBaseFee writes it with. Run with -race.
func TestReplacementCheck_BaseFeeReadIsRaceFree(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	require.NoError(t, pool.addTx(local, newTx(addr1, 0, 1)))
	pool.handlePromoteRequest(<-pool.promoteReqCh)

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for i := 0; i < 2000; i++ {
			pool.SetBaseFee(&types.Header{Number: 1, BaseFee: uint64(i)})
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < 2000; i++ {
			_ = pool.addTx(local, newTx(addr1, 0, 1)) // same-nonce replacement path
		}
	}()

	wg.Wait()
}

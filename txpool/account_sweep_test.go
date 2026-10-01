package txpool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

func sweepAddr(i int) types.Address {
	var a types.Address
	a[0], a[1], a[2], a[3] = byte(i), byte(i>>8), byte(i>>16), 0xfe

	return a
}

func liveAccounts(p *TxPool) int {
	n := 0
	p.accounts.Range(func(_, _ interface{}) bool { n++; return true })

	return n
}

// TX-M1: accounts were never removed, so fresh addresses grew the pool's
// memory without bound even after all their txs were gone.
func TestSweepEmptyAccounts_RemovesEmpty(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	for i := 0; i < 500; i++ {
		tx := newTx(sweepAddr(i), 0, 1)
		require.NoError(t, pool.addTx(local, tx))
		pool.Drop(tx)
	}

	require.Equal(t, 500, liveAccounts(pool))

	pool.sweepEmptyAccounts()

	require.Equal(t, 0, liveAccounts(pool))
	require.Equal(t, uint64(0), atomic.LoadUint64(&pool.accounts.count))
}

// An account whose tx sits in a block not yet imported has a pool nonce ahead
// of the chain; removing it would change pending-nonce answers. It stays.
func TestSweepEmptyAccounts_KeepsAccountAheadOfChain(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	tx := newTx(addr1, 0, 1)
	require.NoError(t, pool.addTx(local, tx))
	pool.handlePromoteRequest(<-pool.promoteReqCh)

	pool.Prepare()
	pool.Pop(pool.Peek()) // in a block being built, not imported yet

	require.Equal(t, uint64(1), pool.GetNonce(addr1))

	pool.sweepEmptyAccounts()

	require.NotNil(t, pool.accounts.get(addr1), "pool nonce 1 vs chain nonce 0: keep")
	require.Equal(t, uint64(1), pool.GetNonce(addr1))
}

// Adding txs while sweeps run must never lose a tx onto a removed account.
// Run with -race.
func TestSweepEmptyAccounts_ConcurrentWithAddTx(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	var (
		wg    sync.WaitGroup
		stop  = make(chan struct{})
		added sync.Map
	)

	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				pool.sweepEmptyAccounts()
			}
		}
	}()

	for g := 0; g < 8; g++ {
		wg.Add(1)

		go func(g int) {
			defer wg.Done()

			for i := 0; i < 2000; i++ {
				tx := newTx(sweepAddr(g*10000+i), 0, 1)
				if pool.addTx(local, tx) == nil {
					added.Store(tx.Hash, tx.From)
				}
			}
		}(g)
	}

	wg.Wait()
	close(stop)

	missing := 0

	added.Range(func(k, v interface{}) bool {
		hash, _ := k.(types.Hash)
		from, _ := v.(types.Address)

		_, inIndex := pool.index.get(hash)
		acc := pool.accounts.get(from)

		if !inIndex || acc == nil || acc.enqueued.length()+acc.promoted.length() == 0 {
			missing++
		}

		return true
	})

	require.Zero(t, missing, "every accepted tx must still be in the pool")
}

// Deterministic version: a sweep is forced into the gap between addTx finding
// a brand-new (empty) account and queuing the tx on it. With the lock the
// sweep waits; without it the account would be removed and the tx lost.
func TestSweepEmptyAccounts_CannotRemoveAccountMidAdd(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	swept := make(chan struct{})

	testHookAfterAccountLookup = func(p *TxPool) {
		go func() {
			p.sweepEmptyAccounts()
			close(swept)
		}()

		select {
		case <-swept:
			t.Error("sweep ran while addTx held an account it had not filled yet")
		case <-time.After(100 * time.Millisecond): // sweep is waiting for addTx: correct
		}
	}
	defer func() { testHookAfterAccountLookup = nil }()

	tx := newTx(addr1, 0, 1)
	require.NoError(t, pool.addTx(local, tx))
	<-swept

	acc := pool.accounts.get(addr1)
	require.NotNil(t, acc, "account removed while its tx was being added")
	require.Equal(t, uint64(1), acc.enqueued.length()+acc.promoted.length())
}

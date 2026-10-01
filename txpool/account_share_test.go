package txpool

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

// TP-M1: three senders used to be able to fill the whole pool (the first took
// 80% before any share limit applied), after which an honest tx was refused
// even at a 1,000,000x price.
func TestAccountShare_FewSendersCannotFillPool(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})

	fill := func(addr types.Address) int {
		n := 0

		for nonce := uint64(0); ; nonce++ {
			tx := newTx(addr, nonce, 1)
			tx.GasPrice, tx.Gas, tx.Input, tx.To, tx.Value = big.NewInt(1), 21_000, nil, &addr5, big.NewInt(0)
			tx.R, tx.S = new(big.Int).SetUint64(nonce+1), new(big.Int).SetUint64(uint64(addr[0]))

			if pool.addTx(local, tx) != nil {
				return n
			}

			pool.handlePromoteRequest(promoteRequest{account: addr})
			n++
		}
	}

	share := int(pool.gauge.max / maxAccountShareDivisor)

	for _, a := range []types.Address{addr1, addr2, addr3} {
		require.Equal(t, share, fill(a), "each sender stops at its share")
	}

	honest := newTx(addr5, 0, 1)
	honest.GasPrice, honest.Gas, honest.Input, honest.To = big.NewInt(1_000_000), 21_000, nil, &addr1
	require.NoError(t, pool.addTx(local, honest), "an honest tx still gets in")
}

// TP-M1: on validators, txs that are never included used to hold their slots
// forever. An account none of whose txs made it into maxValidatorAccountSkips
// blocks is now dropped; one block short of that it is kept.
func TestValidatorSkips_ExpireNeverIncludedAccount(t *testing.T) {
	pool, err := newTestPool()
	require.NoError(t, err)
	pool.SetSigner(&mockSigner{})
	pool.SetSealing(true)

	tx := newTx(addr1, 0, 1)
	require.NoError(t, pool.addTx(local, tx))
	pool.handlePromoteRequest(<-pool.promoteReqCh)

	acc := pool.accounts.get(addr1)
	acc.skips = maxValidatorAccountSkips - 2

	pool.updateAccountSkipsCounts(map[types.Address]uint64{}, maxValidatorAccountSkips)

	_, kept := pool.index.get(tx.Hash)
	require.True(t, kept, "one block short of the limit: still in the pool")

	pool.updateAccountSkipsCounts(map[types.Address]uint64{}, maxValidatorAccountSkips)

	_, kept = pool.index.get(tx.Hash)
	require.False(t, kept, "at the limit: dropped")
	require.Zero(t, pool.gauge.read())
}

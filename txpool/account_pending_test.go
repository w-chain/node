package txpool

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/helper/tests"
	"github.com/w-chain-team/node/types"
)

// balanceStore is the default mock store with a chosen account balance.
type balanceStore struct {
	defaultMockStore
	balance *big.Int
}

func (s balanceStore) GetBalance(types.Hash, types.Address) (*big.Int, error) {
	return new(big.Int).Set(s.balance), nil
}

func newPendingTestPool(t *testing.T, maxSlots uint64, balance *big.Int) (*TxPool, func(*types.Transaction) *types.Transaction, types.Address) {
	t.Helper()

	pool, err := newTestPoolWithSlots(maxSlots, balanceStore{defaultMockStore{DefaultHeader: mockHeader}, balance})
	require.NoError(t, err)

	signer := crypto.NewEIP155Signer(100, true)
	pool.SetSigner(signer)

	key, addr := tests.GenerateKeyAndAddr(t)

	return pool, func(tx *types.Transaction) *types.Transaction {
		signed, err := signer.SignTx(tx, key)
		require.NoError(t, err)

		return signed
	}, addr
}

var addr0 = types.StringToAddress("0x0")

// S-M2: the balance must cover all of an account's pending transactions
// together. Before, a balance enough for ONE transaction backed any number.
func TestAddTx_CumulativeBalance(t *testing.T) {
	t.Parallel()

	priced := func(nonce uint64) *types.Transaction {
		tx := newTx(addr0, nonce, 1)
		tx.GasPrice = big.NewInt(1_000)

		return tx
	}

	one := priced(0).Cost()
	pool, sign, addr := newPendingTestPool(t, 4096, new(big.Int).Mul(one, big.NewInt(3))) // funds for 3
	at := func(tx *types.Transaction) *types.Transaction { tx.From = addr; return sign(tx) }

	for nonce := uint64(0); nonce < 3; nonce++ {
		require.NoError(t, pool.addTx(local, at(priced(nonce))), "tx %d is covered", nonce)
	}

	require.ErrorIs(t, pool.addTx(local, at(priced(3))), ErrInsufficientFunds,
		"a 4th transaction is not covered by the balance")

	// A replacement is counted instead of the transaction it replaces, not on top.
	doubled := priced(2)
	doubled.GasPrice = big.NewInt(2_000)
	require.ErrorIs(t, pool.addTx(local, at(doubled)), ErrInsufficientFunds, "doubling the price needs more funds")

	sameCost := priced(2)
	sameCost.GasPrice = big.NewInt(1_100)
	sameCost.Gas = sameCost.Gas * 10 / 11
	require.NoError(t, pool.addTx(local, at(sameCost)), "a +10% replacement that costs no more is allowed")
}

// S-M2: while the pool is nearly full, one account cannot grow past 1/8 of it.
func TestAddTx_AccountShareUnderPressure(t *testing.T) {
	t.Parallel()

	const maxSlots = 64 // share = 8

	rich := new(big.Int).Lsh(big.NewInt(1), 200)
	pool, sign, addr := newPendingTestPool(t, maxSlots, rich)

	// With room in the pool there is no per-account limit: a batch of 20 is fine.
	for nonce := uint64(0); nonce < 20; nonce++ {
		require.NoError(t, pool.addTx(local, sign(newTx(addr, nonce, 1))), "batch tx %d", nonce)
	}

	// The pool promotes in the background; here we mark all 20 as ready.
	pool.accounts.get(addr).setNonce(20)

	// Push the pool over the pressure mark with other slots.
	pool.gauge.increase(maxSlots*highPressureMark/100 - 20 + 1)
	require.True(t, pool.gauge.highPressure())

	require.ErrorIs(t, pool.addTx(local, sign(newTx(addr, 20, 1))), ErrAccountPoolShareFull)

	// Another account still gets in.
	pool2Tx := func() error {
		key, a := tests.GenerateKeyAndAddr(t)
		signed, err := crypto.NewEIP155Signer(100, true).SignTx(newTx(a, 0, 1), key)
		require.NoError(t, err)

		return pool.addTx(local, signed)
	}
	require.NoError(t, pool2Tx(), "a different sender is not blocked by the flooder")
}

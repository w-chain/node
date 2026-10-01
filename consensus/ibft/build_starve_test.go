package ibft

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/consensus/ibft/hook"
	"github.com/w-chain-team/node/state"
	"github.com/w-chain-team/node/txpool"
	"github.com/w-chain-team/node/types"
)

type starveStore struct{ hdr *types.Header }

func (s starveStore) Header() *types.Header                     { return s.hdr }
func (s starveStore) GetNonce(types.Hash, types.Address) uint64 { return 0 }
func (s starveStore) GetBalance(types.Hash, types.Address) (*big.Int, error) {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil), nil
}
func (s starveStore) GetBlockByHash(types.Hash, bool) (*types.Block, bool) { return nil, false }
func (s starveStore) CalculateBaseFee(*types.Header) uint64                { return 0 }

type starveSigner struct{}

func (starveSigner) Sender(tx *types.Transaction) (types.Address, error) { return tx.From, nil }

// starveTransition has the gas-pool rule of state.Transition.apply:
// subGasPool(msg.Gas) else GasLimitReached; then refund unused gas.
type starveTransition struct {
	pool     uint64
	included []*types.Transaction
}

func (t *starveTransition) Write(tx *types.Transaction) error {
	if t.pool < tx.Gas {
		return state.NewGasLimitReachedTransitionApplicationError(errors.New("gas limit reached"))
	}

	t.pool -= 21_000 // every tx here is a plain transfer: uses 21k regardless of its gas limit
	t.included = append(t.included, tx)

	return nil
}

func (t *starveTransition) GasPool() uint64 { return t.pool }

// TP-H2: block building stopped at the first tx whose gas limit did not fit,
// so an account holding two top-priced txs that each claim the whole block
// kept every block down to a single tx while everyone else waited.
func TestWriteTransactions_BigGasLimitTxDoesNotStarveBlock(t *testing.T) {
	const blockGas = 20_000_000

	forks := chain.AllForksEnabled.Copy()
	forks.RemoveFork(chain.London)

	pool, err := txpool.NewTxPool(hclog.NewNullLogger(), forks,
		starveStore{&types.Header{GasLimit: blockGas}}, nil, nil,
		&txpool.Config{PriceLimit: 1, MaxSlots: 4096, MaxAccountEnqueued: 128})
	require.NoError(t, err)
	pool.SetSigner(starveSigner{})
	pool.Start()
	defer pool.Close()

	attacker := types.Address{0xaa}
	mk := func(from types.Address, nonce, gas, price uint64) *types.Transaction {
		to := types.Address{0x99}
		return &types.Transaction{From: from, To: &to, Nonce: nonce, Gas: gas,
			GasPrice: new(big.Int).SetUint64(price), Value: big.NewInt(0),
			Input: append(from.Bytes(), byte(nonce))}
	}

	// attacker: 2 txs, top price, each claiming the WHOLE block gas, each really using 21k
	require.NoError(t, pool.AddTx(mk(attacker, 0, blockGas, 2000)))
	require.NoError(t, pool.AddTx(mk(attacker, 1, blockGas, 2000)))

	// 300 honest transfers from 300 accounts at a lower price
	for i := 0; i < 300; i++ {
		require.NoError(t, pool.AddTx(mk(types.Address{0x10, byte(i), byte(i >> 8)}, 0, 30_000, 1000)))
	}

	require.Eventually(t, func() bool { return pool.Length() == 302 }, 3*time.Second, 10*time.Millisecond)

	b := &backendIBFT{logger: hclog.NewNullLogger(), txpool: pool}
	b.currentModules.Store(&ibftModules{hooks: &hook.Hooks{}})

	tr := &starveTransition{pool: blockGas}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	executed := b.writeTransactions(ctx, blockGas, 1, tr)

	// The attacker's first tx fits (21k used); its second claims 20M and no
	// longer fits, so its account is skipped and the honest txs go in.
	require.Len(t, executed, 301, "attacker's first tx + all 300 honest txs")
}

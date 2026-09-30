package state_test

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
)

const v109Block = 50

var (
	v109Burn     = types.StringToAddress("0x4b69cE5EA29A8Deac0997Ac6Ae15cb0DFbC7144f")
	v109Coinbase = types.StringToAddress("0xC0FFEE0000000000000000000000000000000001")
	v109User     = types.StringToAddress("0x1111111111111111111111111111111111111111")
	v109Recv     = types.StringToAddress("0x2222222222222222222222222222222222222222")
	v109Contract = types.StringToAddress("0xC0DE000000000000000000000000000000000001")
)

type v109Env struct {
	exec *state.Executor
	st   state.State
	root types.Hash
}

// newV109Env builds a chain with every fork on from genesis except
// WChainV109, which activates at v109Block.
func newV109Env(t *testing.T, alloc map[types.Address]*chain.GenesisAccount) *v109Env {
	t.Helper()

	forks := chain.AllForksEnabled.Copy()
	(*forks)[chain.WChainV109] = chain.NewFork(v109Block)

	params := &chain.Params{ChainID: 171717, Forks: forks, BurnContract: map[uint64]types.Address{0: v109Burn}}
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(params, st, hclog.NewNullLogger())
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.Hash{} } }

	root, err := ex.WriteGenesis(alloc, types.ZeroHash)
	require.NoError(t, err)

	return &v109Env{exec: ex, st: st, root: root}
}

func (e *v109Env) balance(t *testing.T, root types.Hash, addr types.Address) *big.Int {
	t.Helper()

	snap, err := e.st.NewSnapshotAt(root)
	require.NoError(t, err)

	acct, err := snap.GetAccount(addr)
	require.NoError(t, err)

	if acct == nil {
		return big.NewInt(0)
	}

	return acct.Balance
}

func v109Header(number uint64) *types.Header {
	return &types.Header{Number: number, GasLimit: 20_000_000, BaseFee: chain.MinGasPrice, Timestamp: number * 2}
}

// E-H2: a dynamic-fee tx with zero fee cap and tip used to skip the base fee
// check and mint the burn + coinbase credit from nothing. Old behaviour is
// kept before the fork so history replays identically.
func TestV109_ZeroFeeDynamicTx(t *testing.T) {
	t.Parallel()

	zeroFeeTx := func() *types.Transaction {
		tx := &types.Transaction{
			Type: types.DynamicFeeTx, ChainID: big.NewInt(171717), From: v109User, To: &v109Recv,
			Gas: 21_000, GasFeeCap: big.NewInt(0), GasTipCap: big.NewInt(0), Value: big.NewInt(0),
		}
		tx.ComputeHash(1)

		return tx
	}

	e := newV109Env(t, map[types.Address]*chain.GenesisAccount{v109User: {Balance: big.NewInt(0)}})

	// Before the fork: accepted, and the supply grows (the old bug, unchanged).
	txn, err := e.exec.BeginTxn(e.root, v109Header(v109Block-1), v109Coinbase)
	require.NoError(t, err)
	require.NoError(t, txn.Write(zeroFeeTx()))

	_, root, err := txn.Commit()
	require.NoError(t, err)
	require.Positive(t, e.balance(t, root, v109Burn).Sign())

	// From the fork: rejected, nothing is credited.
	txn, err = e.exec.BeginTxn(e.root, v109Header(v109Block), v109Coinbase)
	require.NoError(t, err)
	require.ErrorContains(t, txn.Write(zeroFeeTx()), state.ErrFeeCapTooLow.Error())

	// A legacy tx priced below the base fee is rejected too.
	under := &types.Transaction{
		Type: types.LegacyTx, From: v109User, To: &v109Recv, Gas: 21_000,
		GasPrice: new(big.Int).SetUint64(chain.MinGasPrice - 1), Value: big.NewInt(0),
	}
	under.ComputeHash(1)

	txn, err = e.exec.BeginTxn(e.root, v109Header(v109Block), v109Coinbase)
	require.NoError(t, err)
	require.ErrorContains(t, txn.Write(under), state.ErrFeeCapTooLow.Error())
}

// eth_call (NonPayable) still runs a zero-fee call after the fork.
func TestV109_ZeroFeeCallStillWorks(t *testing.T) {
	t.Parallel()

	e := newV109Env(t, map[types.Address]*chain.GenesisAccount{v109User: {Balance: big.NewInt(0)}})

	txn, err := e.exec.BeginTxn(e.root, v109Header(v109Block+10), v109Coinbase)
	require.NoError(t, err)
	txn.SetNonPayable(true)

	tx := &types.Transaction{
		Type: types.DynamicFeeTx, ChainID: big.NewInt(171717), From: v109User, To: &v109Recv,
		Gas: 21_000, GasFeeCap: big.NewInt(0), GasTipCap: big.NewInt(0), Value: big.NewInt(0),
	}

	res, err := txn.Apply(tx)
	require.NoError(t, err)
	require.NoError(t, res.Err)
}

// E-L1: BYTE with an index of 2^64+31 returned the last byte instead of 0.
func TestV109_ByteHugeIndex(t *testing.T) {
	t.Parallel()

	// PUSH1 0xAB, PUSH9 2^64+31, BYTE, PUSH1 0, MSTORE, PUSH1 32, PUSH1 0, RETURN
	code := []byte{0x60, 0xAB, 0x68, 0x01, 0, 0, 0, 0, 0, 0, 0, 0x1f, 0x1a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}

	e := newV109Env(t, map[types.Address]*chain.GenesisAccount{
		v109User:     {Balance: new(big.Int).Lsh(big.NewInt(1), 200)},
		v109Contract: {Code: code},
	})

	byteAt := func(number uint64) *big.Int {
		txn, err := e.exec.BeginTxn(e.root, v109Header(number), v109Coinbase)
		require.NoError(t, err)

		res := txn.Call2(v109User, v109Contract, nil, big.NewInt(0), 100_000)
		require.NoError(t, res.Err)

		return new(big.Int).SetBytes(res.ReturnValue)
	}

	require.Equal(t, int64(0xAB), byteAt(v109Block-1).Int64(), "old result kept before the fork")
	require.Equal(t, int64(0), byteAt(v109Block).Int64(), "spec result from the fork")
}

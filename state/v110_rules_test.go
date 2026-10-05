package state_test

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/contracts"
	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
)

// v110Block is where WChainV110 activates in these tests. Blocks before it
// must keep the old results so history replays identically.
const v110Block = 50

func newV110Env(t *testing.T, alloc map[types.Address]*chain.GenesisAccount) *v109Env {
	t.Helper()

	forks := chain.AllForksEnabled.Copy()
	(*forks)[chain.WChainV110] = chain.NewFork(v110Block)

	params := &chain.Params{ChainID: 171717, Forks: forks, BurnContract: map[uint64]types.Address{0: v109Burn}}
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(params, st, hclog.NewNullLogger())
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.Hash{} } }

	root, err := ex.WriteGenesis(alloc, types.ZeroHash)
	require.NoError(t, err)

	return &v109Env{exec: ex, st: st, root: root}
}

func v110Rich() *chain.GenesisAccount {
	return &chain.GenesisAccount{Balance: new(big.Int).Mul(big.NewInt(1e18), big.NewInt(1e6))}
}

func v110Tx(nonce uint64, to *types.Address, value int64, input []byte) *types.Transaction {
	return &types.Transaction{
		Type: types.LegacyTx, From: v109User, To: to, Gas: 1_000_000, Nonce: nonce,
		GasPrice: new(big.Int).SetUint64(chain.MinGasPrice * 2), Value: big.NewInt(value), Input: input,
	}
}

func word32(v int64) []byte {
	b := make([]byte, 32)
	big.NewInt(v).FillBytes(b)

	return b
}

// runBlock applies txs as one block at height number and returns the receipts
// and the state root.
func runBlock(
	t *testing.T, e *v109Env, number uint64, txs []*types.Transaction,
) ([]*types.Receipt, types.Hash, *state.Transition) {
	t.Helper()

	txn, err := e.exec.BeginTxn(e.root, v109Header(number), v109Coinbase)
	require.NoError(t, err)

	for _, tx := range txs {
		require.NoError(t, txn.Write(tx))
	}

	_, root, err := txn.Commit()
	require.NoError(t, err)

	return txn.Receipts(), root, txn
}

// EVM-M1: SSTORE took the "original" value from the start of the block while
// the refund counter starts again with each transaction. A slot cleared by an
// earlier transaction made the next one subtract a refund it never got, the
// counter wrapped, and the transaction got half its gas back.
func TestV110_SstoreOriginalPerTx(t *testing.T) {
	t.Parallel()

	// SSTORE(0, CALLDATALOAD(0))
	contract := types.StringToAddress("0xC0DE0000000000000000000000000000000000AA")
	alloc := func() map[types.Address]*chain.GenesisAccount {
		return map[types.Address]*chain.GenesisAccount{
			v109User: v110Rich(),
			contract: {
				Code:    []byte{0x60, 0x00, 0x35, 0x60, 0x00, 0x55, 0x00},
				Storage: map[types.Hash]types.Hash{{}: types.BytesToHash(word32(5))},
			},
		}
	}

	// slot 5 -> 0 in tx1, then 0 -> 7 in tx2 of the same block
	txs := func() []*types.Transaction {
		return []*types.Transaction{v110Tx(0, &contract, 0, word32(0)), v110Tx(1, &contract, 0, word32(7))}
	}

	before, _, _ := runBlock(t, newV110Env(t, alloc()), v110Block-1, txs())
	require.Less(t, before[1].GasUsed, uint64(21_000), "old rule kept before the fork: refund wraps")

	after, _, _ := runBlock(t, newV110Env(t, alloc()), v110Block, txs())
	// As in geth: tx2 creates a slot with no refund. 21000 intrinsic + 140
	// calldata + 9 for PUSH1/CALLDATALOAD/PUSH1 + 20000 SSTORE.
	require.Equal(t, uint64(41_149), after[1].GasUsed, "tx2 pays the full slot creation")
}

// EVM-M2: a self-destructed contract's code stayed in the code cache (keyed
// by address) for the rest of the block, so calling the address again ran
// the dead code.
func TestV110_SelfdestructedCodeNotRunAgain(t *testing.T) {
	t.Parallel()

	// SSTORE(0,42); if calldatasize != 0 { SELFDESTRUCT(caller) }
	a := types.StringToAddress("0xC0DE0000000000000000000000000000000000BB")
	alloc := func() map[types.Address]*chain.GenesisAccount {
		return map[types.Address]*chain.GenesisAccount{
			v109User: v110Rich(),
			a:        {Code: []byte{0x60, 0x2a, 0x60, 0x00, 0x55, 0x36, 0x60, 0x0a, 0x57, 0x00, 0x5b, 0x33, 0xff}},
		}
	}

	txs := func() []*types.Transaction {
		return []*types.Transaction{
			v110Tx(0, &a, 0, []byte{1}), // destroy
			v110Tx(1, &a, 1, nil),       // revive with a transfer
			v110Tx(2, &a, 0, nil),       // call again
		}
	}

	before, _, _ := runBlock(t, newV110Env(t, alloc()), v110Block-1, txs())
	require.NotEqual(t, uint64(21_000), before[2].GasUsed, "old rule kept before the fork: dead code runs")

	e := newV110Env(t, alloc())
	after, root, _ := runBlock(t, e, v110Block, txs())
	require.Equal(t, uint64(21_000), after[2].GasUsed, "no code runs at the destroyed address")

	snap, err := e.st.NewSnapshotAt(root)
	require.NoError(t, err)

	acct, err := snap.GetAccount(a)
	require.NoError(t, err)
	require.Equal(t, types.Hash{}, snap.GetStorage(a, acct.Root, types.Hash{}), "no storage written by dead code")
}

// EVM-H1: EIP-3860 from WChainV110 — init code limit and word price, both for
// create transactions and for CREATE/CREATE2.
func TestV110_InitCodeLimit(t *testing.T) {
	t.Parallel()

	// create tx: limit
	big := make([]byte, state.MaxInitCodeSize+1)

	for _, number := range []uint64{v110Block - 1, v110Block} {
		e := newV110Env(t, map[types.Address]*chain.GenesisAccount{v109User: v110Rich()})
		txn, err := e.exec.BeginTxn(e.root, v109Header(number), v109Coinbase)
		require.NoError(t, err)

		tx := v110Tx(0, nil, 0, big)
		tx.Gas = 5_000_000
		err = txn.Write(tx)

		if number < v110Block {
			require.NoError(t, err, "old rule kept before the fork")
		} else {
			require.EqualError(t, err, state.ErrMaxInitCodeSize.Error())
		}
	}

	// create tx: intrinsic gas pays 2 per init code word from the fork
	tx := v110Tx(0, nil, 0, make([]byte, 64))
	old, err := state.TransactionGasCost(tx, true, true, false)
	require.NoError(t, err)
	v110, err := state.TransactionGasCost(tx, true, true, true)
	require.NoError(t, err)
	require.Equal(t, old+2*2, v110)

	// CREATE with 49153 bytes of init code: PUSH3 0x00c001 PUSH1 0 PUSH1 0 CREATE
	// STOP. The frame fails from the fork on.
	factory := types.StringToAddress("0xC0DE0000000000000000000000000000000000CC")
	code := []byte{0x62, 0x00, 0xc0, 0x01, 0x60, 0x00, 0x60, 0x00, 0xf0, 0x00}

	for _, number := range []uint64{v110Block - 1, v110Block} {
		e := newV110Env(t, map[types.Address]*chain.GenesisAccount{v109User: v110Rich(), factory: {Code: code}})
		tx := v110Tx(0, &factory, 0, nil)
		tx.Gas = 10_000_000
		r, _, _ := runBlock(t, e, number, []*types.Transaction{tx})

		if number < v110Block {
			require.Equal(t, uint64(types.ReceiptSuccess), uint64(*r[0].Status))
		} else {
			require.Equal(t, uint64(types.ReceiptFailed), uint64(*r[0].Status))
		}
	}
}

// EVM-L1: spec edge cases match geth from WChainV110 (cases from the audit's
// compat test; gethFails is what geth does).
func TestV110_SpecEdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		code      []byte
		gethFails bool
	}{
		{"SIGNEXTEND with 1 stack item", []byte{0x60, 0x00, 0x0b, 0x00}, true},
		{"RETURN(2^64, 0)", []byte{0x60, 0x00, 0x68, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0xf3}, false},
		{"KECCAK256(2^64, 0)", []byte{0x60, 0x00, 0x68, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0x20, 0x00}, false},
		{"CALLDATACOPY(2^64, 0, 0)", []byte{0x60, 0x00, 0x60, 0x00, 0x68, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0x37, 0x00}, false},
		{"CALL retOffset=2^64 retSize=0", []byte{0x60, 0x00, 0x68, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x04, 0x61, 0xff, 0xff, 0xf1, 0x00}, false},
		{"RETURNDATACOPY(0,1,0) empty returndata", []byte{0x60, 0x00, 0x60, 0x01, 0x60, 0x00, 0x3e, 0x00}, true},
	}

	for _, tc := range cases {
		e := newV110Env(t, map[types.Address]*chain.GenesisAccount{v109User: v110Rich(), v109Contract: {Code: tc.code}})

		txn, err := e.exec.BeginTxn(e.root, v109Header(v110Block), v109Coinbase)
		require.NoError(t, err)

		res := txn.Call2(v109User, v109Contract, nil, big.NewInt(0), 100_000)
		require.Equal(t, tc.gethFails, res.Failed(), tc.name)
	}
}

// E3-L1: a zero-priced state transaction has a negative tip when a base fee
// is set, which took balance from the coinbase. State transactions only come
// from PolyBFT, and IBFT rejects them in proposals, so this is defensive.
func TestV110_NoNegativeCoinbaseTip(t *testing.T) {
	t.Parallel()

	coinbaseStart := new(big.Int).Mul(big.NewInt(1e18), big.NewInt(10))

	for _, number := range []uint64{v110Block - 1, v110Block} {
		e := newV110Env(t, map[types.Address]*chain.GenesisAccount{v109Coinbase: {Balance: coinbaseStart}})

		tx := &types.Transaction{
			Type: types.StateTx, From: contracts.SystemCaller, To: &v109Recv,
			Gas: types.StateTransactionGasLimit, GasPrice: big.NewInt(0), Value: big.NewInt(0),
		}
		_, root, _ := runBlock(t, e, number, []*types.Transaction{tx})

		coinbase := e.balance(t, root, v109Coinbase)
		if number < v110Block {
			require.Equal(t, -1, coinbase.Cmp(coinbaseStart), "old rule kept before the fork: coinbase loses balance")
		} else {
			require.Equal(t, 0, coinbase.Cmp(coinbaseStart), "coinbase never pays for a transaction")
		}
	}
}

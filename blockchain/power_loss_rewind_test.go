package blockchain

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/blockchain/storage/leveldb"
	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
	"github.com/w-chain-team/node/types/buildroot"
)

// Audit BI-M1: a power loss keeps block 1 and the head pointer in the
// blockchain database but loses block 1's state in the trie database. The
// node used to start on head 1 and fail every following block forever.
func TestRewindToState_PowerLoss(t *testing.T) {
	prodEx := state.NewExecutor(plParams(), itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
	prodEx.GetHash = plNoHash
	root0, err := prodEx.WriteGenesis(plAlloc(), types.ZeroHash)
	require.NoError(t, err)

	genesis := &chain.Genesis{GasLimit: 20_000_000, StateRoot: root0, Alloc: plAlloc()}
	g := genesis.GenesisHeader()
	g.ComputeHash()

	block1 := plBuild(t, prodEx, g, 0)
	block2 := plBuild(t, prodEx, block1.Header, 1)

	dir := t.TempDir()

	n := plOpen(t, dir, genesis)
	_, err = n.ex.WriteGenesis(plAlloc(), types.ZeroHash)
	require.NoError(t, err)
	require.NoError(t, n.b.ComputeGenesis())
	n.close()

	snap := filepath.Join(t.TempDir(), "trie-before-block1")
	require.NoError(t, os.CopyFS(snap, os.DirFS(filepath.Join(dir, "trie"))))

	n = plOpen(t, dir, genesis)
	require.NoError(t, n.b.ComputeGenesis())
	fb, err := n.b.VerifyFinalizedBlock(block1)
	require.NoError(t, err)
	require.NoError(t, n.b.WriteFullBlock(fb, "syncer"))
	n.close()

	// Power loss: block 1's state never reached disk, block 1 did.
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "trie")))
	require.NoError(t, os.CopyFS(filepath.Join(dir, "trie"), os.DirFS(snap)))

	n = plOpen(t, dir, genesis)
	defer n.close()

	require.NoError(t, n.b.ComputeGenesis())
	require.Equal(t, uint64(1), n.b.Header().Number)

	hasState := func(root types.Hash) bool {
		_, err := n.ex.StateAt(root)

		return err == nil
	}

	require.NoError(t, n.b.RewindToState(hasState))
	require.Equal(t, uint64(0), n.b.Header().Number)

	_, ok := n.b.GetHeaderByNumber(1)
	require.False(t, ok, "block 1 is no longer canonical")

	// The syncer refills: block 1 and 2 import again.
	for _, blk := range []*types.Block{block1, block2} {
		fb, err := n.b.VerifyFinalizedBlock(blk)
		require.NoError(t, err)
		require.NoError(t, n.b.WriteFullBlock(fb, "syncer"))
	}

	require.Equal(t, uint64(2), n.b.Header().Number)
	require.True(t, hasState(n.b.Header().StateRoot))

	// Nothing to do when the head state is there; the head survives a restart.
	require.NoError(t, n.b.RewindToState(hasState))
	require.Equal(t, uint64(2), n.b.Header().Number)
}

var (
	plSender   = types.StringToAddress("0x1111")
	plReceiver = types.StringToAddress("0x2222")
	plCoinbase = types.StringToAddress("0xc0c0")
)

func plParams() *chain.Params {
	return &chain.Params{ChainID: 1, Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: {}}}
}

func plAlloc() map[types.Address]*chain.GenesisAccount {
	return map[types.Address]*chain.GenesisAccount{
		plSender: {Balance: new(big.Int).Lsh(big.NewInt(1), 100)},
	}
}

func plNoHash(*types.Header) state.GetHashByNumber {
	return func(uint64) types.Hash { return types.Hash{} }
}

func plBuild(t *testing.T, ex *state.Executor, parent *types.Header, nonce uint64) *types.Block {
	t.Helper()

	header := &types.Header{
		ParentHash: parent.Hash, Number: parent.Number + 1, GasLimit: parent.GasLimit,
		BaseFee: chain.MinGasPrice, Difficulty: parent.Number + 1, Timestamp: parent.Timestamp + 2,
		Sha3Uncles: types.EmptyUncleHash, Miner: plCoinbase.Bytes(),
	}

	tx := &types.Transaction{
		From: plSender, To: &plReceiver, Nonce: nonce, Gas: 21000,
		GasPrice: new(big.Int).SetUint64(chain.MinGasPrice), Value: big.NewInt(1),
	}
	tx.ComputeHash(header.Number)

	txn, err := ex.BeginTxn(parent.StateRoot, header, plCoinbase)
	require.NoError(t, err)
	require.NoError(t, txn.Write(tx))

	_, root, err := txn.Commit()
	require.NoError(t, err)

	header.StateRoot = root
	header.GasUsed = txn.TotalGas()
	header.ReceiptsRoot = buildroot.CalculateReceiptsRoot(txn.Receipts())
	header.TxRoot = buildroot.CalculateTransactionsRoot([]*types.Transaction{tx}, header.Number)
	header.ComputeHash()

	return &types.Block{Header: header, Transactions: []*types.Transaction{tx}}
}

type plNode struct {
	b    *Blockchain
	ex   *state.Executor
	trie itrie.Storage
}

func plOpen(t *testing.T, dir string, genesis *chain.Genesis) *plNode {
	t.Helper()

	return plOpenParams(t, dir, genesis, plParams())
}

func plOpenParams(t *testing.T, dir string, genesis *chain.Genesis, params *chain.Params) *plNode {
	t.Helper()

	trie, err := itrie.NewLevelDBStorage(filepath.Join(dir, "trie"), hclog.NewNullLogger())
	require.NoError(t, err)

	db, err := leveldb.NewLevelDBStorage(filepath.Join(dir, "blockchain"), hclog.NewNullLogger())
	require.NoError(t, err)

	ex := state.NewExecutor(params, itrie.NewState(trie), hclog.NewNullLogger())

	b, err := NewBlockchain(hclog.NewNullLogger(), db,
		&chain.Chain{Genesis: genesis, Params: params}, &MockVerifier{}, ex, &mockSigner{})
	require.NoError(t, err)

	ex.GetHash = b.GetHashHelper

	return &plNode{b: b, ex: ex, trie: trie}
}

func (n *plNode) close() {
	n.b.Close()
	n.trie.Close()
}

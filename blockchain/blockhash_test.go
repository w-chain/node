package blockchain

import (
	"math/big"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/blockchain/storage"
	"github.com/w-chain-team/node/blockchain/storage/leveldb"
	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
)

// newHeaderChain stores n linked headers and returns them, oldest first.
func newHeaderChain(t *testing.T, n uint64) (*Blockchain, []*types.Header) {
	t.Helper()

	db, err := leveldb.NewLevelDBStorage(t.TempDir(), hclog.NewNullLogger())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	b := &Blockchain{logger: hclog.NewNullLogger(), db: db}
	require.NoError(t, b.initCaches(defaultCacheSize))

	headers := make([]*types.Header, 0, n)
	parent := types.Hash{}

	for i := uint64(0); i < n; i++ {
		h := &types.Header{ParentHash: parent, Number: i, GasLimit: 20_000_000, ExtraData: make([]byte, 900), Timestamp: i * 2}
		h.ExtraData[0] = byte(i)
		h.ComputeHash()

		bw := storage.NewBatchWriter(db)
		bw.PutHeader(h)
		require.NoError(t, bw.WriteBatch())

		parent = h.Hash
		headers = append(headers, h)
	}

	return b, headers
}

// walkHash is the original GetHashHelper walk, kept as the reference result.
func walkHash(b *Blockchain, header *types.Header, i uint64) (res types.Hash) {
	num, hash := header.Number-1, header.ParentHash

	for {
		if num == i {
			return hash
		}

		h, ok := b.GetHeaderByHash(hash)
		if !ok {
			return
		}

		hash = h.ParentHash

		if num == 0 {
			return
		}

		num--
	}
}

// The cached lookup must return exactly what the plain walk returned, for
// every number, in any query order — block execution depends on it.
func TestGetHashHelper_MatchesWalk(t *testing.T) {
	t.Parallel()

	b, headers := newHeaderChain(t, 400)
	tip := &types.Header{ParentHash: headers[len(headers)-1].Hash, Number: 400}

	get := b.GetHashHelper(tip)

	// Deepest first, then everything in both directions, plus out-of-range.
	order := []uint64{0, 143, 399}
	for i := uint64(0); i <= 420; i++ {
		order = append(order, i)
	}

	for i := int64(420); i >= 0; i-- {
		order = append(order, uint64(i))
	}

	for _, i := range order {
		require.Equal(t, walkHash(b, tip, i), get(i), "number %d", i)
	}

	require.Equal(t, headers[399].Hash, get(399))
	require.Equal(t, headers[144].Hash, get(144))
	require.Equal(t, types.Hash{}, get(400), "the block itself has no hash yet")
}

// A missing ancestor gives the zero hash, as before, and does not poison
// later lookups of numbers above the gap.
func TestGetHashHelper_MissingAncestor(t *testing.T) {
	t.Parallel()

	b, headers := newHeaderChain(t, 10)

	// Child of an unknown parent: only the direct parent hash is known.
	tip := &types.Header{ParentHash: types.StringToHash("0xdead"), Number: 50}
	get := b.GetHashHelper(tip)

	require.Equal(t, walkHash(b, tip, 20), get(20))
	require.Equal(t, types.StringToHash("0xdead"), get(49))
	require.Equal(t, walkHash(b, tip, 48), get(48))

	tip2 := &types.Header{ParentHash: headers[9].Hash, Number: 10}
	require.Equal(t, headers[3].Hash, b.GetHashHelper(tip2)(3))
}

// E-C1: a contract looping BLOCKHASH(NUMBER-256) used to take ~57s per 1M gas
// (a 20M-gas block ran for ~19 minutes), stalling every proposer in turn.
func TestGetHashHelper_BlockhashLoopIsCheap(t *testing.T) {
	t.Parallel()

	b, headers := newHeaderChain(t, 400)

	// n = NUMBER-256; loop { (DUP1 BLOCKHASH POP) x 100 }
	code := []byte{0x43, 0x61, 0x01, 0x00, 0x90, 0x03}
	start := len(code)
	code = append(code, 0x5b)

	for i := 0; i < 100; i++ {
		code = append(code, 0x80, 0x40, 0x50)
	}

	code = append(code, 0x61, byte(start>>8), byte(start), 0x56)

	contract := types.StringToAddress("0xC0DE")
	sender := types.StringToAddress("0x1111")
	params := &chain.Params{ChainID: 1, Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: {}}}

	ex := state.NewExecutor(params, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
	ex.GetHash = b.GetHashHelper

	root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{
		sender:   {Balance: new(big.Int).Lsh(big.NewInt(1), 200)},
		contract: {Code: code},
	}, types.ZeroHash)
	require.NoError(t, err)

	header := &types.Header{ParentHash: headers[len(headers)-1].Hash, Number: 400, GasLimit: 20_000_000, BaseFee: chain.MinGasPrice}
	txn, err := ex.BeginTxn(root, header, types.Address{})
	require.NoError(t, err)

	tx := &types.Transaction{From: sender, To: &contract, Gas: 1_000_000, GasPrice: new(big.Int).SetUint64(chain.MinGasPrice), Value: big.NewInt(0)}
	tx.ComputeHash(1)

	begin := time.Now()
	require.NoError(t, txn.Write(tx))
	took := time.Since(begin)

	require.Equal(t, uint64(1_000_000), txn.Receipts()[0].GasUsed, "the loop runs until it is out of gas")
	require.Less(t, took, 2*time.Second, "1M gas of BLOCKHASH must not take seconds")
	t.Logf("1M gas of BLOCKHASH(NUMBER-256): %v", took)
}

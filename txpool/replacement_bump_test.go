package txpool

import (
	"math/big"
	"testing"

	"github.com/golang/protobuf/ptypes/any"
	"github.com/stretchr/testify/require"
	protobuf "google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/txpool/proto"
	"github.com/w-chain-team/node/types"
)

// S-L1: a replacement must pay at least 10% more, as in geth.
func TestIsReplacementPriceBumped(t *testing.T) {
	t.Parallel()

	old := big.NewInt(1_000)

	require.False(t, isReplacementPriceBumped(old, big.NewInt(1_000)), "same price")
	require.False(t, isReplacementPriceBumped(old, big.NewInt(1_001)), "+1 wei")
	require.False(t, isReplacementPriceBumped(old, big.NewInt(1_099)), "just under 10%")
	require.True(t, isReplacementPriceBumped(old, big.NewInt(1_100)), "exactly 10%")
	require.True(t, isReplacementPriceBumped(old, big.NewInt(5_000)))
	require.False(t, isReplacementPriceBumped(big.NewInt(0), big.NewInt(0)))
	require.True(t, isReplacementPriceBumped(big.NewInt(0), big.NewInt(1)))
}

// S-L1: oversized or malformed gossip transactions are not relayed.
func TestIsRelayableGossipTx(t *testing.T) {
	t.Parallel()

	pool, err := newTestPool() // no signer yet: structural checks only
	require.NoError(t, err)

	tx := &types.Transaction{Nonce: 1, GasPrice: big.NewInt(1), Gas: 21_000, Value: big.NewInt(0), V: big.NewInt(1), R: big.NewInt(1), S: big.NewInt(1)}

	require.True(t, pool.isRelayableGossipTx(wrapGossipTx(t, tx.MarshalRLP())), "honest tx")
	require.False(t, pool.isRelayableGossipTx(wrapGossipTx(t, make([]byte, types.MaxRawTxSize+1))), "oversized")
	require.False(t, pool.isRelayableGossipTx([]byte{0xff, 0xff}), "not a Txn message")

	empty, err := protobuf.Marshal(&proto.Txn{})
	require.NoError(t, err)
	require.False(t, pool.isRelayableGossipTx(empty), "no raw payload")
}

func wrapGossipTx(t *testing.T, raw []byte) []byte {
	t.Helper()

	b, err := protobuf.Marshal(&proto.Txn{Raw: &any.Any{Value: raw}})
	require.NoError(t, err)

	return b
}

// TP-M2: unfunded and junk-signed txs used to be relayed by every node, each
// one costing every node an ecrecover, state reads and an ERROR log line.
// A tx is now relayed only if it would be accepted over RPC.
func TestIsRelayableGossipTx_RunsPoolChecks(t *testing.T) {
	t.Parallel()

	pool, sign, addr := newPendingTestPool(t, defaultMaxSlots, new(big.Int).Lsh(big.NewInt(1), 100))

	good := sign(newTx(addr, 0, 1))
	require.True(t, pool.isRelayableGossipTx(wrapGossipTx(t, good.MarshalRLP())), "valid, funded, signed")

	junk := newTx(addr, 0, 1)
	junk.V, junk.R, junk.S = big.NewInt(27), big.NewInt(0), big.NewInt(0) // recovers no key
	require.False(t, pool.isRelayableGossipTx(wrapGossipTx(t, junk.MarshalRLP())), "junk signature")

	poor, signPoor, poorAddr := newPendingTestPool(t, defaultMaxSlots, big.NewInt(0))
	unfunded := signPoor(newTx(poorAddr, 0, 1))
	require.False(t, poor.isRelayableGossipTx(wrapGossipTx(t, unfunded.MarshalRLP())), "sender cannot pay")

	cheap := newTx(addr, 0, 1)
	cheap.GasPrice = big.NewInt(0)
	require.False(t, pool.isRelayableGossipTx(wrapGossipTx(t, sign(cheap).MarshalRLP())), "below the price limit")
}

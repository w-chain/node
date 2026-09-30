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

	wrap := func(raw []byte) []byte {
		b, err := protobuf.Marshal(&proto.Txn{Raw: &any.Any{Value: raw}})
		require.NoError(t, err)

		return b
	}

	tx := &types.Transaction{Nonce: 1, GasPrice: big.NewInt(1), Gas: 21_000, Value: big.NewInt(0), V: big.NewInt(1), R: big.NewInt(1), S: big.NewInt(1)}

	require.True(t, isRelayableGossipTx(wrap(tx.MarshalRLP())), "honest tx")
	require.False(t, isRelayableGossipTx(wrap(make([]byte, types.MaxRawTxSize+1))), "oversized")
	require.False(t, isRelayableGossipTx([]byte{0xff, 0xff}), "not a Txn message")

	empty, err := protobuf.Marshal(&proto.Txn{})
	require.NoError(t, err)
	require.False(t, isRelayableGossipTx(empty), "no raw payload")
}

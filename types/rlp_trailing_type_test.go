package types

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/fastrlp"
)

// P2-C1: a transaction list ending in a lone EIP-2718 type byte indexed past
// the end of the list and panicked. It came from peers during sync, where
// nothing recovered it, so one crafted block crashed the node.
func TestUnmarshalRLP_TrailingTypeByte(t *testing.T) {
	t.Parallel()

	ar := &fastrlp.Arena{}
	blk := ar.NewArray()
	blk.Set((&Header{Number: 1, ExtraData: []byte{1}}).MarshalRLPWith(ar))

	txs := ar.NewArray()
	txs.Set(ar.NewBytes([]byte{0x02})) // type byte with no body after it
	blk.Set(txs)
	blk.Set(ar.NewNullArray())

	require.NotPanics(t, func() {
		require.ErrorContains(t, (&Block{}).UnmarshalRLP(blk.MarshalTo(nil)), "missing its body")
	})

	require.NotPanics(t, func() {
		require.ErrorContains(t, (&Receipts{}).UnmarshalRLP([]byte{0xc1, 0x02}), "missing its body")
	})
}

package jsonrpc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

// RPC-M1: txpool_content returned every tx with its full input, so a full
// pool made one ~250 MB response. Input past the budget is now omitted.
func TestTxpoolContent_InputBudget(t *testing.T) {
	const perTx = 128 << 10 // the largest tx the pool accepts

	n := maxContentInputBytes/perTx + 20
	txs := make([]*types.Transaction, 0, n)

	for i := 0; i < n; i++ {
		tx := newTestTransaction(uint64(i), addr1)
		tx.Input = make([]byte, perTx)
		txs = append(txs, tx)
	}

	store := newMockTxPoolStore()
	store.pending = map[types.Address][]*types.Transaction{addr1: txs}

	res, err := (&TxPool{store}).Content()
	require.NoError(t, err)

	total, withInput := 0, 0

	for _, tx := range res.(ContentResponse).Pending[addr1] { //nolint:forcetypeassert
		total += len(tx.Input)
		if len(tx.Input) > 0 {
			withInput++
		}
	}

	require.Len(t, res.(ContentResponse).Pending[addr1], n, "every tx is still listed") //nolint:forcetypeassert
	require.LessOrEqual(t, total, maxContentInputBytes)
	require.Equal(t, maxContentInputBytes/perTx, withInput)
}

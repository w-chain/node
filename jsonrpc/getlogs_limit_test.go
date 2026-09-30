package jsonrpc

import (
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

// newLogsStore builds blocks 1..numBlocks with logsPerBlock matching logs each.
func newLogsStore(numBlocks, logsPerBlock int) *mockBlockStore {
	store := &mockBlockStore{receipts: map[types.Hash][]*types.Receipt{}}
	blocks := []*types.Block{{Header: &types.Header{Number: 0, Hash: types.StringToHash("0x0")}}}

	for i := 1; i <= numBlocks; i++ {
		h := types.StringToHash(fmt.Sprintf("0x%x", 0x1000+i))
		blocks = append(blocks, &types.Block{
			Header:       &types.Header{Number: uint64(i), Hash: h},
			Transactions: []*types.Transaction{{Hash: h}},
		})

		logs := make([]*types.Log, logsPerBlock)
		for j := range logs {
			logs[j] = &types.Log{Topics: []types.Hash{hash1}}
		}

		store.receipts[h] = []*types.Receipt{{Logs: logs, TxHash: h}}
	}

	store.appendBlocksToStore(blocks)

	return store
}

// P2-M2: one eth_getLogs call could return an unbounded number of logs.
func TestGetLogsForQuery_ResultCap(t *testing.T) {
	t.Parallel()

	// 30 blocks x 4000 logs = 120,000 > cap.
	f := NewFilterManager(hclog.NewNullLogger(), newLogsStore(30, 4000), 1000)
	defer f.Close()

	_, err := f.GetLogsForQuery(&LogQuery{fromBlock: 1, toBlock: 30, Topics: [][]types.Hash{{hash1}}})
	require.ErrorIs(t, err, ErrTooManyLogs)

	// A range under the cap is answered in full.
	got, err := f.GetLogsForQuery(&LogQuery{fromBlock: 1, toBlock: 20, Topics: [][]types.Hash{{hash1}}})
	require.NoError(t, err)
	require.Len(t, got, 80_000)

	// The blockHash form (no range check) is capped too.
	big := newLogsStore(1, maxLogsPerQuery+1)
	fb := NewFilterManager(hclog.NewNullLogger(), big, 1000)
	defer fb.Close()

	blk, _ := big.GetBlockByNumber(1, false)
	_, err = fb.GetLogsForQuery(&LogQuery{BlockHash: &blk.Header.Hash})
	require.ErrorIs(t, err, ErrTooManyLogs)
}

// E-L2: tip + base fee saturates instead of wrapping to a tiny gas price.
func TestSuggestedGasPrice_Saturates(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint64(1601), suggestedGasPrice(big.NewInt(801), 800))
	require.Equal(t, uint64(math.MaxUint64), suggestedGasPrice(big.NewInt(2), math.MaxUint64-1))
	require.Equal(t, uint64(math.MaxUint64), suggestedGasPrice(new(big.Int).Lsh(big.NewInt(1), 70), 1))
}

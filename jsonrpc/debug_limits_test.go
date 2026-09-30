package jsonrpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/state/runtime/tracer"
	"github.com/w-chain-team/node/types"
)

// R-M1: debug_traceCall must honour the same gas cap as eth_call.
func TestTraceCall_GasCap(t *testing.T) {
	t.Parallel()

	from, to := types.StringToAddress("1"), types.StringToAddress("2")
	blockNumber := BlockNumber(testBlock10.Number())

	var traced uint64

	store := &debugEndpointMockStore{
		getHeaderByNumberFn: func(uint64) (*types.Header, bool) {
			return &types.Header{Number: testHeader10.Number, GasLimit: 320_000_000}, true
		},
		traceCallFn: func(tx *types.Transaction, _ *types.Header, _ tracer.Tracer) (interface{}, error) {
			traced = tx.Gas

			return nil, nil
		},
		headerFn:     func() *types.Header { return testLatestHeader },
		getAccountFn: func(types.Hash, types.Address) (*Account, error) { return &Account{}, nil },
	}

	d := NewDebug(store, 100000)
	d.gasCap = 50_000_000

	// No gas given: the block gas limit, then capped.
	_, err := d.TraceCall(&txnArgs{From: &from, To: &to}, BlockNumberOrHash{BlockNumber: &blockNumber}, &TraceConfig{})
	require.NoError(t, err)
	require.Equal(t, uint64(50_000_000), traced)

	small := argUint64(21_000)
	_, err = d.TraceCall(&txnArgs{From: &from, To: &to, Gas: &small}, BlockNumberOrHash{BlockNumber: &blockNumber}, &TraceConfig{})
	require.NoError(t, err)
	require.Equal(t, uint64(21_000), traced, "below the cap is untouched")
}

// R-M1: a caller can shorten the trace timeout but not stretch it past the cap.
func TestClampTraceTimeout(t *testing.T) {
	t.Parallel()

	require.Equal(t, time.Second, clampTraceTimeout(time.Second))
	require.Equal(t, maxTraceTimeout, clampTraceTimeout(maxTraceTimeout))
	require.Equal(t, maxTraceTimeout, clampTraceTimeout(1000*time.Hour))
}

package jsonrpc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

// RPC-M3: a value transfer with no data to a contract runs the contract's
// receive/fallback code. eth_estimateGas used to answer 21000 without running
// it, so wallets sent transfers that ran out of gas and lost the fee.
func TestEstimateGas_ValueTransferToContractIsExecuted(t *testing.T) {
	store := getExampleStore()
	contract := addr0
	store.account.code = []byte{0x60, 0x00, 0x80, 0xfd} // PUSH1 0 DUP1 REVERT

	called := 0
	store.applyTxnHook = func(_ *types.Header, _ *types.Transaction) (*runtime.ExecutionResult, error) {
		called++

		return &runtime.ExecutionResult{Err: runtime.ErrExecutionReverted}, nil
	}

	e := newTestEthEndpoint(store)
	from := types.StringToAddress("0x1234")
	tx := constructMockTx(nil, nil)
	tx.Value = argBytesPtr([]byte{0x1})
	tx.From = &from
	tx.To = &contract

	_, err := e.EstimateGas(tx, nil)
	require.Error(t, err, "a transfer the contract reverts must not be estimated at 21000")
	require.Positive(t, called, "the contract code must be executed")
}

// A plain transfer to an account with no code keeps the 21000 shortcut.
func TestEstimateGas_ValueTransferToEOAShortcut(t *testing.T) {
	store := getExampleStore()
	store.account.code = nil

	called := 0
	store.applyTxnHook = func(_ *types.Header, _ *types.Transaction) (*runtime.ExecutionResult, error) {
		called++

		return &runtime.ExecutionResult{}, nil
	}

	e := newTestEthEndpoint(store)
	from, to := types.StringToAddress("0x1234"), types.StringToAddress("0x5678")
	tx := constructMockTx(nil, nil)
	tx.Value = argBytesPtr([]byte{0x1})
	tx.From = &from
	tx.To = &to

	est, err := e.EstimateGas(tx, nil)
	require.NoError(t, err)
	require.Equal(t, argUint64(21000), est)
	require.Zero(t, called)
}

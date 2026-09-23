package calltracer

import (
	"errors"
	"math/big"
	"testing"

	"github.com/w-chain-team/node/helper/hex"
	"github.com/w-chain-team/node/types"
	"github.com/stretchr/testify/require"
)

func TestCallTracer_Cancel(t *testing.T) {
	t.Parallel()

	err := errors.New("timeout")

	tracer := &CallTracer{}

	require.Nil(t, tracer.reason)
	require.False(t, tracer.stop)
	require.False(t, tracer.cancelled())

	tracer.Cancel(err)

	require.Equal(t, err, tracer.reason)
	require.True(t, tracer.stop)
	require.True(t, tracer.cancelled())
}

// A reverted call must still produce a usable trace. Previously CallEnd cancelled the
// tracer on any call error, so GetResult returned an error instead of the trace — which
// made debug_traceTransaction fail outright for every reverted transaction and poisoned
// batched internal-transaction indexing. The genuine cancel path (timeouts) is unaffected.
func TestCallTracer_RevertedCallStillReturnsTrace(t *testing.T) {
	t.Parallel()

	revertErr := errors.New("execution reverted")

	tracer := &CallTracer{}
	tracer.CallStart(1, types.StringToAddress("1"), types.StringToAddress("2"),
		4, 1000, big.NewInt(0), []byte("initcode"))
	tracer.CallEnd(1, []byte("output"), revertErr)

	res, err := tracer.GetResult()

	// The trace is returned, not swallowed by an error.
	require.NoError(t, err)
	require.NotNil(t, res)

	call, ok := res.(*Call)
	require.True(t, ok)

	// The revert is reported on the frame, and the frame is otherwise intact.
	require.Equal(t, revertErr.Error(), call.Error)
	require.Equal(t, "CREATE", call.Type)
	require.Equal(t, hex.EncodeToHex([]byte("initcode")), call.Input)

	// The tracer was not put into a cancelled state.
	require.False(t, tracer.cancelled())
}

// The timeout path must still abort the trace.
func TestCallTracer_CancelStillAbortsTrace(t *testing.T) {
	t.Parallel()

	timeoutErr := errors.New("timeout")

	tracer := &CallTracer{}
	tracer.CallStart(1, types.StringToAddress("1"), types.StringToAddress("2"),
		0, 1000, big.NewInt(0), []byte("input"))
	tracer.Cancel(timeoutErr)

	res, err := tracer.GetResult()

	require.Error(t, err)
	require.Equal(t, timeoutErr, err)
	require.Nil(t, res)
}

func TestCallTracer_Clear(t *testing.T) {
	t.Parallel()

	tracer := &CallTracer{}
	tracer.call = &Call{}

	tracer.Clear()

	require.Nil(t, tracer.call)
}

// CallStart function correctly initializes a new Call object with the provided parameters
func TestCallTracer_CallStart(t *testing.T) {
	t.Parallel()

	t.Run("call_start_initializes_call_object", func(t *testing.T) {
		t.Parallel()

		c := &CallTracer{}

		var (
			depth    = 1
			from     = types.StringToAddress("0xFrom")
			to       = types.StringToAddress("0xTo")
			callType = 0
			gas      = uint64(100000)
			value    = big.NewInt(100)
			input    = []byte("input")
		)

		c.CallStart(depth, from, to, callType, gas, value, input)

		expectedCall := &Call{
			Type:     "CALL",
			From:     from.String(),
			To:       to.String(),
			Value:    "0x64",
			Gas:      "0x186a0",
			GasUsed:  "",
			Input:    "0x696e707574",
			Output:   "",
			Calls:    nil,
			startGas: gas,
		}

		require.Equal(t, expectedCall, c.call)
	})

	t.Run("call_start_sets_parent_when_depth_greater_than_1", func(t *testing.T) {
		t.Parallel()

		c := &CallTracer{}

		var (
			depth    = 2
			from     = types.StringToAddress("0xFrom")
			to       = types.StringToAddress("0xTo")
			callType = 0
			gas      = uint64(100000)
			value    = big.NewInt(100)
			input    = []byte("input")
		)

		parentCall := &Call{
			Type:     "CALL",
			From:     from.String(),
			To:       to.String(),
			Value:    "0x64",
			Gas:      "0x186a0",
			GasUsed:  "",
			Input:    "0x696e707574",
			Output:   "",
			Calls:    nil,
			startGas: gas,
		}
		c.activeCall = parentCall

		c.CallStart(depth, from, to, callType, gas, value, input)

		expectedCall := &Call{
			Type:     "CALL",
			From:     from.String(),
			To:       to.String(),
			Value:    "0x64",
			Gas:      "0x186a0",
			GasUsed:  "",
			Input:    "0x696e707574",
			Output:   "",
			Calls:    nil,
			startGas: gas,
			parent:   parentCall,
		}

		require.Equal(t, expectedCall, c.activeCall)
		require.Equal(t, []*Call{expectedCall}, parentCall.Calls)
	})

	t.Run("call_start_handles_nil_value_parameter_for_value_and_input", func(t *testing.T) {
		t.Parallel()

		c := &CallTracer{}

		var (
			depth    = 1
			from     = types.Address{}
			to       = types.Address{}
			callType = 0
			gas      = uint64(100000)
		)

		c.CallStart(depth, from, to, callType, gas, nil, nil)

		expectedCall := &Call{
			Type:     "CALL",
			From:     from.String(),
			To:       to.String(),
			Value:    "0x0",
			Gas:      "0x186a0",
			GasUsed:  "",
			Input:    "0x",
			Output:   "",
			Calls:    nil,
			startGas: gas,
		}

		require.Equal(t, expectedCall, c.call)
	})

	t.Run("call_start_handles_unknown_call_type", func(t *testing.T) {
		t.Parallel()

		c := &CallTracer{}

		var (
			depth    = 1
			from     = types.Address{}
			to       = types.Address{}
			callType = 999 // unknown type
			gas      = uint64(100000)
			value    = big.NewInt(100)
			input    = []byte("input")
		)

		c.CallStart(depth, from, to, callType, gas, value, input)

		expectedCall := &Call{
			Type:     "UNKNOWN",
			From:     from.String(),
			To:       to.String(),
			Value:    "0x64",
			Gas:      "0x186a0",
			GasUsed:  "",
			Input:    "0x696e707574",
			Output:   "",
			Calls:    nil,
			startGas: gas,
		}

		require.Equal(t, expectedCall, c.call)
	})
}

func TestCallTracer_CallEnd(t *testing.T) {
	t.Parallel()

	output := []byte("output")
	err := errors.New("error")

	t.Run("call_end_when_depth_is_1_no_error_activeAvailableGas_higher_than_start_gas", func(t *testing.T) {
		t.Parallel()

		tracer := &CallTracer{}
		tracer.activeAvailableGas = 2000
		tracer.activeCall = &Call{
			startGas: 1000,
		}

		tracer.CallEnd(1, output, nil)

		require.Equal(t, uint64(0), tracer.activeGas)
		require.Equal(t, hex.EncodeToHex(output), tracer.activeCall.Output)
		require.Equal(t, "0x0", tracer.activeCall.GasUsed)
	})

	t.Run("call_end_when_depth_is_1_error_activeAvailableGas_higher_than_start_gas", func(t *testing.T) {
		t.Parallel()

		tracer := &CallTracer{}
		tracer.activeAvailableGas = 2000
		tracer.activeCall = &Call{
			startGas: 1000,
		}

		tracer.CallEnd(1, output, err)

		require.Equal(t, uint64(0), tracer.activeGas)
		require.Equal(t, hex.EncodeToHex(output), tracer.activeCall.Output)
		require.Equal(t, "0x0", tracer.activeCall.GasUsed)
		// A failed call is recorded on the frame, not treated as a tracer abort, so the
		// trace stays intact and GetResult still returns it.
		require.Equal(t, err.Error(), tracer.activeCall.Error)
		require.False(t, tracer.stop)
		require.NoError(t, tracer.reason)
	})

	t.Run("call_end_when_depth_is_1_no_error_activeAvailableGas_lower_than_start_gas", func(t *testing.T) {
		t.Parallel()

		tracer := &CallTracer{}
		tracer.activeAvailableGas = 1000
		tracer.activeCall = &Call{
			startGas: 2000,
		}

		tracer.CallEnd(1, output, nil)

		require.Equal(t, uint64(0), tracer.activeGas)
		require.Equal(t, hex.EncodeToHex(output), tracer.activeCall.Output)
		require.Equal(t, hex.EncodeUint64(1000), tracer.activeCall.GasUsed)
	})

	t.Run("call_end_when_depth_is_1_error_activeAvailableGas_lower_than_start_gas", func(t *testing.T) {
		t.Parallel()

		tracer := &CallTracer{}
		tracer.activeAvailableGas = 1000
		tracer.activeCall = &Call{
			startGas: 2000,
		}

		tracer.CallEnd(1, output, err)

		require.Equal(t, uint64(0), tracer.activeGas)
		require.Equal(t, hex.EncodeToHex(output), tracer.activeCall.Output)
		require.Equal(t, hex.EncodeUint64(1000), tracer.activeCall.GasUsed)
		// A failed call is recorded on the frame, not treated as a tracer abort, so the
		// trace stays intact and GetResult still returns it.
		require.Equal(t, err.Error(), tracer.activeCall.Error)
		require.False(t, tracer.stop)
		require.NoError(t, tracer.reason)
	})

	t.Run("call_end_when_depth_is_2_no_error", func(t *testing.T) {
		t.Parallel()

		tracer := &CallTracer{}
		tracer.activeAvailableGas = 2000
		tracer.activeCall = &Call{
			startGas: 1000,
			parent: &Call{
				startGas: 500,
				Output:   hex.EncodeToHex(output),
				GasUsed:  "0x0",
			},
		}

		tracer.CallEnd(2, output, nil)

		require.Equal(t, uint64(0), tracer.activeGas)
		require.Equal(t, hex.EncodeToHex(output), tracer.activeCall.Output)
		require.Equal(t, "0x0", tracer.activeCall.GasUsed)
		require.Equal(t, uint64(500), tracer.activeCall.startGas)
	})
}

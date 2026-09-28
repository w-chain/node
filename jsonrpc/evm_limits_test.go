package jsonrpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCapCallGas(t *testing.T) {
	t.Parallel()

	const capGas = 50_000_000

	require.Equal(t, uint64(21_000), capCallGas(21_000, capGas), "below the cap is untouched")
	require.Equal(t, uint64(capGas), capCallGas(capGas, capGas), "exactly the cap is untouched")
	require.Equal(t, uint64(capGas), capCallGas(320_000_000, capGas), "testnet block limit is capped")
	require.Equal(t, uint64(20_000_000), capCallGas(20_000_000, capGas), "mainnet block limit is below the cap")
	require.Equal(t, uint64(320_000_000), capCallGas(320_000_000, 0), "0 disables the cap")
}

type haltRecorder struct{ halted bool }

func (h *haltRecorder) Halt() { h.halted = true }

func TestDeadlineTracer_Disabled(t *testing.T) {
	t.Parallel()

	d := newDeadlineTracer(0)

	// A disabled deadline must attach no tracer at all (not a typed nil,
	// which the executor would treat as a tracer being set).
	require.Nil(t, d.asTracer())
	require.False(t, d.timedOut())
	d.stop() // must not panic
}

func TestDeadlineTracer_HaltsOnlyAfterDeadline(t *testing.T) {
	t.Parallel()

	d := newDeadlineTracer(100 * time.Millisecond)
	defer d.stop()

	require.NotNil(t, d.asTracer())

	before := &haltRecorder{}
	d.CaptureState(nil, nil, 0, [20]byte{}, 0, nil, before)
	require.False(t, before.halted, "must not halt before the deadline")
	require.False(t, d.timedOut())

	require.Eventually(t, d.timedOut, time.Second, 10*time.Millisecond)

	after := &haltRecorder{}
	d.CaptureState(nil, nil, 0, [20]byte{}, 0, nil, after)
	require.True(t, after.halted, "must halt the EVM once the deadline passed")
}

func TestDeadlineTracer_StopPreventsExpiry(t *testing.T) {
	t.Parallel()

	d := newDeadlineTracer(50 * time.Millisecond)
	d.stop()

	time.Sleep(100 * time.Millisecond)
	require.False(t, d.timedOut(), "a finished request must not be marked as timed out")
}

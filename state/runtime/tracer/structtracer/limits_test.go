package structtracer

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// RPC-M2: struct logs had no memory bound; a deep stack copied every step
// reached ~2 GB within the 5 s timeout. The trace now stops at the cap.
func TestStructTracer_StopsAtLogSizeCap(t *testing.T) {
	t.Parallel()

	tr := NewStructTracer(Config{EnableStack: true, EnableStructLogs: true})

	stack := make([]string, 1000)
	for i := range stack {
		stack[i] = strings.Repeat("f", 64)
	}

	per := structLogSize(nil, stack, nil, "", "")
	for n := uint64(0); n <= maxStructLogBytes/per; n++ {
		tr.addLogBytes(per)
	}

	require.True(t, tr.cancelled())

	_, err := tr.GetResult()
	require.ErrorIs(t, err, ErrTraceTooLarge)
}

// RPC-M2: when a block is traced, Clear runs before each transaction. It
// used to reset a timeout that had fired, so tracing ran on past it.
func TestStructTracer_CancelSurvivesClear(t *testing.T) {
	t.Parallel()

	tr := NewStructTracer(Config{EnableStructLogs: true})
	timeout := errors.New("execution timeout")

	tr.Cancel(timeout)
	tr.Clear()

	require.True(t, tr.cancelled())

	_, err := tr.GetResult()
	require.ErrorIs(t, err, timeout)
}

// A traced block keeps each transaction's result. Clear reused the log slice,
// so the next transaction overwrote the previous result's entries.
func TestStructTracer_ClearDoesNotOverwritePreviousResult(t *testing.T) {
	t.Parallel()

	tr := NewStructTracer(Config{EnableStructLogs: true})
	tr.logs = append(tr.logs, StructLog{Pc: 1}, StructLog{Pc: 2})

	first, err := tr.GetResult()
	require.NoError(t, err)

	tr.Clear()
	tr.logs = append(tr.logs, StructLog{Pc: 99})

	firstLogs := first.(*StructTraceResult).StructLogs //nolint:forcetypeassert
	require.Equal(t, uint64(1), firstLogs[0].Pc, "the first transaction's result must be unchanged")
}

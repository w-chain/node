package jsonrpc

import (
	"math/big"
	"sync/atomic"
	"time"

	"github.com/w-chain-team/node/state/runtime/tracer"
	"github.com/w-chain-team/node/types"
)

// capCallGas limits the gas of an eth_call / eth_estimateGas execution.
// Callers first default an unset gas to the block gas limit, as before; this
// then lowers it to gasCap when higher, so one request cannot keep the node
// busy for as long as a whole block's gas lasts. gasCap == 0 disables it.
func capCallGas(gas, gasCap uint64) uint64 {
	if gasCap != 0 && gas > gasCap {
		return gasCap
	}

	return gas
}

// deadlineTracer halts the EVM once its deadline passes. It is attached only
// to RPC executions (eth_call, eth_estimateGas); block processing never has a
// tracer, so consensus execution is unaffected.
//
// It uses the same mechanism as the debug_* timeout: on every opcode the EVM
// hands the tracer a VMState, and a cancelled tracer calls Halt() on it. That
// is the only way to stop a running execution — cutting the HTTP response
// (as nginx does) leaves the node computing to the end.
type deadlineTracer struct {
	expired atomic.Bool
	timer   *time.Timer
}

// newDeadlineTracer returns nil when timeout is 0 (disabled).
func newDeadlineTracer(timeout time.Duration) *deadlineTracer {
	if timeout <= 0 {
		return nil
	}

	t := &deadlineTracer{}
	t.timer = time.AfterFunc(timeout, func() { t.expired.Store(true) })

	return t
}

// stop releases the timer; call it once the request is done.
func (t *deadlineTracer) stop() {
	if t != nil {
		t.timer.Stop()
	}
}

// timedOut reports whether the execution was halted by the deadline.
func (t *deadlineTracer) timedOut() bool {
	return t != nil && t.expired.Load()
}

// asTracer returns the tracer to attach, or nil (no tracer) when disabled.
// Returning a typed nil inside the interface would make the executor think a
// tracer is set.
func (t *deadlineTracer) asTracer() tracer.Tracer {
	if t == nil {
		return nil
	}

	return t
}

func (t *deadlineTracer) Cancel(error)                    { t.expired.Store(true) }
func (t *deadlineTracer) Clear()                          {}
func (t *deadlineTracer) GetResult() (interface{}, error) { return nil, nil }
func (t *deadlineTracer) TxStart(uint64)                  {}
func (t *deadlineTracer) TxEnd(uint64)                    {}
func (t *deadlineTracer) CallEnd(int, []byte, error)      {}
func (t *deadlineTracer) CallStart(int, types.Address, types.Address, int, uint64, *big.Int, []byte) {
}

func (t *deadlineTracer) CaptureState(
	_ []byte, _ []*big.Int, _ int, _ types.Address, _ int, _ tracer.RuntimeHost, state tracer.VMState,
) {
	if t.expired.Load() {
		state.Halt()
	}
}

func (t *deadlineTracer) ExecuteState(
	types.Address, uint64, string, uint64, uint64, []byte, int, error, tracer.RuntimeHost,
) {
}

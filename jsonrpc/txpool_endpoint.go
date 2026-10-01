package jsonrpc

import (
	"fmt"
	"strconv"

	"github.com/w-chain-team/node/types"
)

// txPoolStore provides access to the methods needed for txpool endpoint
type txPoolStore interface {
	// GetTxs gets tx pool transactions currently pending for inclusion and currently queued for validation
	GetTxs(inclQueued bool) (map[types.Address][]*types.Transaction, map[types.Address][]*types.Transaction)

	// GetCapacity returns the current and max capacity of the pool in slots
	GetCapacity() (uint64, uint64)

	// GetBaseFee returns current base fee
	GetBaseFee() uint64
}

// TxPool is the txpool jsonrpc endpoint
type TxPool struct {
	store txPoolStore
}

type ContentResponse struct {
	Pending map[types.Address]map[uint64]*transaction `json:"pending"`
	Queued  map[types.Address]map[uint64]*transaction `json:"queued"`
}

type InspectResponse struct {
	Pending         map[string]map[string]string `json:"pending"`
	Queued          map[string]map[string]string `json:"queued"`
	CurrentCapacity uint64                       `json:"currentCapacity"`
	MaxCapacity     uint64                       `json:"maxCapacity"`
}

type StatusResponse struct {
	Pending uint64 `json:"pending"`
	Queued  uint64 `json:"queued"`
}

// maxContentInputBytes bounds the transaction input data one txpool_content
// response carries. A full pool held ~250 MB of input, so one call built a
// ~250 MB response and a batch of them several GB (audit RPC-M1). Txs past
// the budget are listed with every field but their input.
const maxContentInputBytes = 8 << 20

// txpoolDumpSlots lets one txpool_content / txpool_inspect be built at a time.
var txpoolDumpSlots = make(chan struct{}, 1)

// Create response for txpool_content request.
// See https://geth.ethereum.org/docs/rpc/ns-txpool#txpool_content.
func (t *TxPool) Content() (interface{}, error) {
	txpoolDumpSlots <- struct{}{}
	defer func() { <-txpoolDumpSlots }()

	inputBudget := maxContentInputBytes

	convertTxMap := func(txMap map[types.Address][]*types.Transaction) map[types.Address]map[uint64]*transaction {
		result := make(map[types.Address]map[uint64]*transaction, len(txMap))

		for addr, txs := range txMap {
			result[addr] = make(map[uint64]*transaction, len(txs))

			for _, tx := range txs {
				out := toTransaction(tx, nil, &types.ZeroHash, nil)

				if inputBudget -= len(tx.Input); inputBudget < 0 {
					out.Input = argBytes{}
				}

				result[addr][tx.Nonce] = out
			}
		}

		return result
	}

	pendingTxs, queuedTxs := t.store.GetTxs(true)
	resp := ContentResponse{
		Pending: convertTxMap(pendingTxs),
		Queued:  convertTxMap(queuedTxs),
	}

	return resp, nil
}

// Create response for txpool_inspect request.
// See https://geth.ethereum.org/docs/rpc/ns-txpool#txpool_inspect.
func (t *TxPool) Inspect() (interface{}, error) {
	txpoolDumpSlots <- struct{}{}
	defer func() { <-txpoolDumpSlots }()

	baseFee := t.store.GetBaseFee()
	convertTxMap := func(txMap map[types.Address][]*types.Transaction) map[string]map[string]string {
		result := make(map[string]map[string]string, len(txMap))

		for addr, txs := range txMap {
			result[addr.String()] = make(map[string]string, len(txs))

			for _, tx := range txs {
				nonceStr := strconv.FormatUint(tx.Nonce, 10)
				result[addr.String()][nonceStr] = fmt.Sprintf(
					"%d wei + %d gas x %d wei", tx.Value, tx.Gas, tx.GetGasPrice(baseFee),
				)
			}
		}

		return result
	}

	// get capacity of the TxPool
	current, max := t.store.GetCapacity()
	pendingTxs, queuedTxs := t.store.GetTxs(true)
	resp := InspectResponse{
		Pending:         convertTxMap(pendingTxs),
		Queued:          convertTxMap(queuedTxs),
		CurrentCapacity: current,
		MaxCapacity:     max,
	}

	return resp, nil
}

// Create response for txpool_status request.
// See https://geth.ethereum.org/docs/rpc/ns-txpool#txpool_status.
func (t *TxPool) Status() (interface{}, error) {
	pendingTxs, queuedTxs := t.store.GetTxs(true)

	var pendingCount uint64

	for _, t := range pendingTxs {
		pendingCount += uint64(len(t))
	}

	var queuedCount uint64

	for _, t := range queuedTxs {
		queuedCount += uint64(len(t))
	}

	resp := StatusResponse{
		Pending: pendingCount,
		Queued:  queuedCount,
	}

	return resp, nil
}

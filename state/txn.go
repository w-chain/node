package state

import (
	"errors"
	"fmt"
	"math/big"

	iradix "github.com/hashicorp/go-immutable-radix"
	lru "github.com/hashicorp/golang-lru"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

var emptyStateHash = types.StringToHash("0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")

type readSnapshot interface {
	GetStorage(addr types.Address, root types.Hash, key types.Hash) types.Hash
	GetAccount(addr types.Address) (*Account, error)
	GetCode(hash types.Hash) ([]byte, bool)
}

var (
	// logIndex is the index of the logs in the trie
	logIndex = types.BytesToHash([]byte{2}).Bytes()

	// refundIndex is the index of the refund
	refundIndex = types.BytesToHash([]byte{3}).Bytes()
)

// Txn is a reference of the state
type Txn struct {
	snapshot  readSnapshot
	snapshots []*iradix.Tree
	txn       *iradix.Txn
	codeCache *lru.Cache

	// v110 turns on the WChainV110 rules: SSTORE's original value is the one
	// at the start of the transaction, the refund counter never goes below
	// zero, and the code cache is keyed by code hash.
	v110 bool

	// txStart is the state at the start of the current transaction, for the
	// SSTORE original value from WChainV110.
	txStart *iradix.Tree

	// dirty holds the accounts written since the last CleanDeleteObjects.
	// Only they can have become empty or self-destructed, so cleaning checks
	// them instead of walking every account touched so far in the block,
	// which made a block quadratic in touched accounts (audit ST-H1).
	dirty map[string]struct{}
}

func (txn *Txn) markDirty(key []byte) {
	if txn.dirty == nil {
		txn.dirty = make(map[string]struct{})
	}

	txn.dirty[string(key)] = struct{}{}
}

func NewTxn(snapshot Snapshot) *Txn {
	return newTxn(snapshot)
}

func (txn *Txn) GetRadix() *iradix.Txn {
	return txn.txn
}

func newTxn(snapshot readSnapshot) *Txn {
	i := iradix.New()

	codeCache, _ := lru.New(20)

	return &Txn{
		snapshot:  snapshot,
		snapshots: []*iradix.Tree{},
		txn:       i.Txn(),
		codeCache: codeCache,
	}
}

// Snapshot takes a snapshot at this point in time
func (txn *Txn) Snapshot() int {
	t := txn.txn.CommitOnly()

	id := len(txn.snapshots)
	txn.snapshots = append(txn.snapshots, t)

	return id
}

// ReleaseSnapshot drops snapshot id and every later one. Snapshots belong to a
// call frame and are only reverted to inside it, and frames finish in reverse
// order, so once a frame returns its snapshot and its inner frames' are never
// used again. Keeping them until the end of the block pinned an old copy of
// the state tree per call: a transaction of many calls held hundreds of MB
// (audit ST-M1).
func (txn *Txn) ReleaseSnapshot(id int) {
	if id < 0 || id >= len(txn.snapshots) {
		return
	}

	for i := id; i < len(txn.snapshots); i++ {
		txn.snapshots[i] = nil // let the old tree be collected
	}

	txn.snapshots = txn.snapshots[:id]
}

// RevertToSnapshot reverts to a given snapshot
func (txn *Txn) RevertToSnapshot(id int) error {
	if id > len(txn.snapshots)-1 {
		return fmt.Errorf("snapshot id %d out of the range", id)
	}

	tree := txn.snapshots[id]
	txn.txn = tree.Txn()

	return nil
}

// GetAccount returns an account
func (txn *Txn) GetAccount(addr types.Address) (*Account, bool) {
	object, exists := txn.getStateObject(addr)
	if !exists {
		return nil, false
	}

	return object.Account, true
}

func (txn *Txn) getStateObject(addr types.Address) (*StateObject, bool) {
	// Try to get state from radix tree which holds transient states during block processing first
	val, exists := txn.txn.Get(addr.Bytes())
	if exists {
		obj := val.(*StateObject) //nolint:forcetypeassert
		if obj.Deleted {
			return nil, false
		}

		return obj.Copy(), true
	}

	account, err := txn.snapshot.GetAccount(addr)
	if err != nil {
		return nil, false
	}

	if account == nil {
		return nil, false
	}

	obj := &StateObject{
		Account: account.Copy(),
	}

	return obj, true
}

func (txn *Txn) upsertAccount(addr types.Address, create bool, f func(object *StateObject)) {
	object, exists := txn.getStateObject(addr)
	if !exists && create {
		object = &StateObject{
			Account: &Account{
				Balance:  big.NewInt(0),
				CodeHash: types.EmptyCodeHash.Bytes(),
				Root:     emptyStateHash,
			},
		}
	}

	// run the callback to modify the account
	f(object)

	if object != nil {
		txn.txn.Insert(addr.Bytes(), object)
		txn.markDirty(addr.Bytes())
	}
}

func (txn *Txn) AddSealingReward(addr types.Address, balance *big.Int) {
	txn.upsertAccount(addr, true, func(object *StateObject) {
		if object.Suicide {
			*object = *newStateObject(txn)
			object.Account.Balance.SetBytes(balance.Bytes())
		} else {
			object.Account.Balance.Add(object.Account.Balance, balance)
		}
	})
}

// AddBalance adds balance
func (txn *Txn) AddBalance(addr types.Address, balance *big.Int) {
	txn.upsertAccount(addr, true, func(object *StateObject) {
		object.Account.Balance.Add(object.Account.Balance, balance)
	})
}

// SubBalance reduces the balance at address addr by amount
func (txn *Txn) SubBalance(addr types.Address, amount *big.Int) error {
	// If we try to reduce balance by 0, then it's a noop
	if amount.Sign() == 0 {
		return nil
	}

	// Check if we have enough balance to deduce amount from
	if balance := txn.GetBalance(addr); balance.Cmp(amount) < 0 {
		return runtime.ErrNotEnoughFunds
	}

	txn.upsertAccount(addr, true, func(object *StateObject) {
		object.Account.Balance.Sub(object.Account.Balance, amount)
	})

	return nil
}

// SetBalance sets the balance
func (txn *Txn) SetBalance(addr types.Address, balance *big.Int) {
	txn.upsertAccount(addr, true, func(object *StateObject) {
		object.Account.Balance.SetBytes(balance.Bytes())
	})
}

// GetBalance returns the balance of an address
func (txn *Txn) GetBalance(addr types.Address) *big.Int {
	object, exists := txn.getStateObject(addr)
	if !exists {
		return big.NewInt(0)
	}

	return object.Account.Balance
}

// EmitLog appends log to logs tree storage
func (txn *Txn) EmitLog(addr types.Address, topics []types.Hash, data []byte) {
	log := &types.Log{
		Address: addr,
		Topics:  topics,
	}
	log.Data = append(log.Data, data...)

	var logs []*types.Log

	val, exists := txn.txn.Get(logIndex)
	if !exists {
		logs = []*types.Log{}
	} else {
		logs = val.([]*types.Log) //nolint:forcetypeassert
	}

	logs = append(logs, log)
	txn.txn.Insert(logIndex, logs)
}

// State

// SetStorage sets the storage of an address
func (txn *Txn) SetStorage(
	addr types.Address,
	key types.Hash,
	value types.Hash,
	config *chain.ForksInTime,
) runtime.StorageStatus {
	oldValue := txn.GetState(addr, key)
	if oldValue == value {
		return runtime.StorageUnchanged
	}

	current := oldValue                          // current - storage dirtied by previous lines of this contract
	original := txn.GetCommittedState(addr, key) // storage slot before this transaction started

	txn.SetState(addr, key, value)

	legacyGasMetering := !config.Istanbul && (config.Petersburg || !config.Constantinople)

	if legacyGasMetering {
		if oldValue == types.ZeroHash {
			return runtime.StorageAdded
		} else if value == types.ZeroHash {
			txn.AddRefund(15000)

			return runtime.StorageDeleted
		}

		return runtime.StorageModified
	}

	if original == current {
		if original == types.ZeroHash { // create slot (2.1.1)
			return runtime.StorageAdded
		}

		if value == types.ZeroHash { // delete slot (2.1.2b)
			txn.AddRefund(15000)

			return runtime.StorageDeleted
		}

		return runtime.StorageModified
	}

	if original != types.ZeroHash { // Storage slot was populated before this transaction started
		if current == types.ZeroHash { // recreate slot (2.2.1.1)
			txn.SubRefund(15000)
		} else if value == types.ZeroHash { // delete slot (2.2.1.2)
			txn.AddRefund(15000)
		}
	}

	if original == value {
		if original == types.ZeroHash { // reset to original nonexistent slot (2.2.2.1)
			// Storage was used as memory (allocation and deallocation occurred within the same contract)
			if config.Istanbul {
				txn.AddRefund(19200)
			} else {
				txn.AddRefund(19800)
			}
		} else { // reset to original existing slot (2.2.2.2)
			if config.Istanbul {
				txn.AddRefund(4200)
			} else {
				txn.AddRefund(4800)
			}
		}
	}

	return runtime.StorageModifiedAgain
}

// SetState change the state of an address
func (txn *Txn) SetState(
	addr types.Address,
	key,
	value types.Hash,
) {
	txn.upsertAccount(addr, true, func(object *StateObject) {
		if object.Txn == nil {
			object.Txn = iradix.New().Txn()
		}

		if value == types.ZeroHash {
			object.Txn.Insert(key.Bytes(), nil)
		} else {
			object.Txn.Insert(key.Bytes(), value.Bytes())
		}
	})
}

// GetState returns the state of the address at a given key
func (txn *Txn) GetState(addr types.Address, key types.Hash) types.Hash {
	object, exists := txn.getStateObject(addr)
	if !exists {
		return types.Hash{}
	}

	// Try to get account state from radix tree first
	// Because the latest account state should be in in-memory radix tree
	// if account state update happened in previous transactions of same block
	if object.Txn != nil {
		if val, ok := object.Txn.Get(key.Bytes()); ok {
			if val == nil {
				return types.Hash{}
			}
			//nolint:forcetypeassert
			return types.BytesToHash(val.([]byte))
		}
	}

	if object.withFakeStorage {
		return types.Hash{}
	}

	return txn.snapshot.GetStorage(addr, object.Account.Root, key)
}

// Nonce

// IncrNonce increases the nonce of the address
func (txn *Txn) IncrNonce(addr types.Address) error {
	var err error

	txn.upsertAccount(addr, true, func(object *StateObject) {
		if object.Account.Nonce+1 < object.Account.Nonce {
			err = ErrNonceUintOverflow

			return
		}
		object.Account.Nonce++
	})

	return err
}

// SetNonce reduces the balance
func (txn *Txn) SetNonce(addr types.Address, nonce uint64) {
	txn.upsertAccount(addr, true, func(object *StateObject) {
		object.Account.Nonce = nonce
	})
}

// GetNonce returns the nonce of an addr
func (txn *Txn) GetNonce(addr types.Address) uint64 {
	object, exists := txn.getStateObject(addr)
	if !exists {
		return 0
	}

	return object.Account.Nonce
}

// Code

// SetCode sets the code for an address
func (txn *Txn) SetCode(addr types.Address, code []byte) {
	txn.upsertAccount(addr, true, func(object *StateObject) {
		object.Account.CodeHash = crypto.Keccak256(code)
		object.DirtyCode = true
		object.Code = code
	})
}

// GetCode gets the code on a given address
func (txn *Txn) GetCode(addr types.Address) []byte {
	object, exists := txn.getStateObject(addr)
	if !exists {
		return nil
	}

	if object.DirtyCode {
		return object.Code
	}
	codeHash := types.BytesToHash(object.Account.CodeHash)

	// Keyed by address, the cache kept a self-destructed contract's code for
	// the rest of the block, so a revived address ran the dead code (audit
	// EVM-M2). From WChainV110 it is keyed by code hash.
	var cacheKey interface{} = addr
	if txn.v110 {
		cacheKey = codeHash
	}

	//nolint:godox
	// TODO; Should we move this to state? (to be fixed in EVM-527)
	v, ok := txn.codeCache.Get(cacheKey)

	if ok {
		//nolint:forcetypeassert
		return v.([]byte)
	}

	code, _ := txn.snapshot.GetCode(codeHash)
	txn.codeCache.Add(cacheKey, code)

	return code
}

func (txn *Txn) GetCodeSize(addr types.Address) int {
	return len(txn.GetCode(addr))
}

func (txn *Txn) GetCodeHash(addr types.Address) types.Hash {
	object, exists := txn.getStateObject(addr)
	if !exists {
		return types.Hash{}
	}

	return types.BytesToHash(object.Account.CodeHash)
}

// Suicide marks the given account as suicided
func (txn *Txn) Suicide(addr types.Address) bool {
	var suicided bool

	txn.upsertAccount(addr, false, func(object *StateObject) {
		if object == nil || object.Suicide {
			suicided = false
		} else {
			suicided = true
			object.Suicide = true
		}
		if object != nil {
			object.Account.Balance = new(big.Int)
		}
	})

	return suicided
}

// HasSuicided returns true if the account suicided
func (txn *Txn) HasSuicided(addr types.Address) bool {
	object, exists := txn.getStateObject(addr)

	return exists && object.Suicide
}

// Refund
func (txn *Txn) AddRefund(gas uint64) {
	refund := txn.GetRefund() + gas
	txn.txn.Insert(refundIndex, refund)
}

func (txn *Txn) SubRefund(gas uint64) {
	current := txn.GetRefund()

	// From WChainV110 the counter stops at zero instead of wrapping around to
	// a huge refund (audit EVM-M1).
	if txn.v110 && gas > current {
		txn.txn.Insert(refundIndex, uint64(0))

		return
	}

	refund := current - gas
	txn.txn.Insert(refundIndex, refund)
}

func (txn *Txn) Logs() []*types.Log {
	data, exists := txn.txn.Get(logIndex)
	if !exists {
		return nil
	}

	txn.txn.Delete(logIndex)
	//nolint:forcetypeassert
	return data.([]*types.Log)
}

func (txn *Txn) GetRefund() uint64 {
	data, exists := txn.txn.Get(refundIndex)
	if !exists {
		return 0
	}

	//nolint:forcetypeassert
	return data.(uint64)
}

// GetCommittedState returns the storage value SSTORE treats as original.
// Before WChainV110 that is the value at the start of the block. The refund
// counter starts again with every transaction, so a slot cleared by an earlier
// transaction of the block subtracted a refund it never added, wrapping the
// counter (audit EVM-M1). From WChainV110 it is the value at the start of the
// transaction, as in Ethereum.
func (txn *Txn) GetCommittedState(addr types.Address, key types.Hash) types.Hash {
	if txn.v110 && txn.txStart != nil {
		return txn.storageAt(txn.txStart, addr, key)
	}

	obj, ok := txn.getStateObject(addr)
	if !ok {
		return types.Hash{}
	}

	return txn.snapshot.GetStorage(addr, obj.Account.Root, key)
}

// storageAt reads a storage slot as it was in tree, an earlier copy of the
// block's state.
func (txn *Txn) storageAt(tree *iradix.Tree, addr types.Address, key types.Hash) types.Hash {
	val, ok := tree.Get(addr.Bytes())
	if !ok {
		account, err := txn.snapshot.GetAccount(addr)
		if err != nil || account == nil {
			return types.Hash{}
		}

		return txn.snapshot.GetStorage(addr, account.Root, key)
	}

	obj := val.(*StateObject) //nolint:forcetypeassert
	if obj.Deleted {
		return types.Hash{}
	}

	if obj.Txn != nil {
		if v, ok := obj.Txn.Get(key.Bytes()); ok {
			if v == nil {
				return types.Hash{}
			}

			return types.BytesToHash(v.([]byte)) //nolint:forcetypeassert
		}
	}

	if obj.withFakeStorage {
		return types.Hash{}
	}

	return txn.snapshot.GetStorage(addr, obj.Account.Root, key)
}

// beginTx records the state a transaction starts from.
func (txn *Txn) beginTx(id int) {
	if txn.v110 && id >= 0 && id < len(txn.snapshots) {
		txn.txStart = txn.snapshots[id]
	}
}

// SetFullStorage is used to replace the full state of the address.
// Only used for debugging on the override jsonrpc endpoint.
func (txn *Txn) SetFullStorage(addr types.Address, state map[types.Hash]types.Hash) {
	for k, v := range state {
		txn.SetState(addr, k, v)
	}

	txn.upsertAccount(addr, true, func(object *StateObject) {
		object.withFakeStorage = true
	})
}

func (txn *Txn) TouchAccount(addr types.Address) {
	txn.upsertAccount(addr, true, func(obj *StateObject) {

	})
}

func (txn *Txn) Exist(addr types.Address) bool {
	_, exists := txn.getStateObject(addr)

	return exists
}

func (txn *Txn) Empty(addr types.Address) bool {
	obj, exists := txn.getStateObject(addr)
	if !exists {
		return true
	}

	return obj.Empty()
}

func newStateObject(txn *Txn) *StateObject {
	return &StateObject{
		Account: &Account{
			Balance:  big.NewInt(0),
			CodeHash: types.EmptyCodeHash.Bytes(),
			Root:     emptyStateHash,
		},
	}
}

func (txn *Txn) CreateAccount(addr types.Address) {
	obj := &StateObject{
		Account: &Account{
			Balance:  big.NewInt(0),
			CodeHash: types.EmptyCodeHash.Bytes(),
			Root:     emptyStateHash,
		},
	}

	prev, ok := txn.getStateObject(addr)
	if ok {
		obj.Account.Balance.SetBytes(prev.Account.Balance.Bytes())
	}

	txn.txn.Insert(addr.Bytes(), obj)
	txn.markDirty(addr.Bytes())
}

func (txn *Txn) CleanDeleteObjects(deleteEmptyObjects bool) error {
	dirty := txn.dirty
	txn.dirty = nil

	for key := range dirty {
		k := []byte(key)

		// A write undone by a reverted call is simply gone.
		v, ok := txn.txn.Get(k)
		if !ok {
			continue
		}

		obj, ok := v.(*StateObject)
		if !ok {
			return errors.New("found object is not of StateObject type")
		}

		// Marking an already deleted object again changes nothing.
		if obj.Deleted || !(obj.Suicide || obj.Empty() && deleteEmptyObjects) {
			continue
		}

		obj2 := obj.Copy()
		obj2.Deleted = true
		txn.txn.Insert(k, obj2)
	}

	// delete refunds
	txn.txn.Delete(refundIndex)

	return nil
}

func (txn *Txn) Commit(deleteEmptyObjects bool) ([]*Object, error) {
	if err := txn.CleanDeleteObjects(deleteEmptyObjects); err != nil {
		return nil, err
	}

	x := txn.txn.Commit()

	// Do a more complex thing for now
	objs := []*Object{}

	x.Root().Walk(func(k []byte, v interface{}) bool {
		a, ok := v.(*StateObject)
		if !ok {
			// We also have logs, avoid those
			return false
		}

		obj := &Object{
			Nonce:     a.Account.Nonce,
			Address:   types.BytesToAddress(k),
			Balance:   a.Account.Balance,
			Root:      a.Account.Root,
			CodeHash:  types.BytesToHash(a.Account.CodeHash),
			DirtyCode: a.DirtyCode,
			Code:      a.Code,
		}
		if a.Deleted {
			obj.Deleted = true
		} else {
			if a.Txn != nil {
				a.Txn.Root().Walk(func(k []byte, v interface{}) bool {
					store := &StorageObject{Key: k}
					if v == nil {
						store.Deleted = true
					} else {
						store.Val = v.([]byte) //nolint:forcetypeassert
					}
					obj.Storage = append(obj.Storage, store)

					return false
				})
			}
		}

		objs = append(objs, obj)

		return false
	})

	return objs, nil
}

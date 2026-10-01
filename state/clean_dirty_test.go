package state_test

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
)

// refCleanDeleteObjects is the original CleanDeleteObjects: it walks every
// account written so far in the block after every transaction.
func refCleanDeleteObjects(txn *state.Txn, deleteEmptyObjects bool) {
	remove := [][]byte{}

	txn.GetRadix().Root().Walk(func(k []byte, v interface{}) bool {
		if a, ok := v.(*state.StateObject); ok && (a.Suicide || a.Empty() && deleteEmptyObjects) {
			remove = append(remove, k)
		}

		return false
	})

	for _, k := range remove {
		v, _ := txn.GetRadix().Get(k)
		obj2 := v.(*state.StateObject).Copy() //nolint:forcetypeassert
		obj2.Deleted = true
		txn.GetRadix().Insert(k, obj2)
	}
}

func normalize(objs []*state.Object) []string {
	out := make([]string, 0, len(objs))

	for _, o := range objs {
		s := fmt.Sprintf("%s nonce=%d bal=%s code=%s root=%s del=%v dirtyCode=%v",
			o.Address, o.Nonce, o.Balance, o.CodeHash, o.Root, o.Deleted, o.DirtyCode)

		for _, st := range o.Storage {
			s += fmt.Sprintf(" [%x=%x del=%v]", st.Key, st.Val, st.Deleted)
		}

		out = append(out, s)
	}

	return out
}

// ST-H1: cleaning only the accounts written during a transaction must give
// exactly the same committed state as walking every account each time.
func TestCleanDeleteObjects_SameResultAsFullWalk(t *testing.T) {
	t.Parallel()

	addrs := make([]types.Address, 24)
	for i := range addrs {
		addrs[i] = types.StringToAddress(fmt.Sprintf("0x%x", 0x1000+i))
	}

	// Pre-state: some accounts exist with balance, nonce, code and storage.
	st := itrie.NewState(itrie.NewMemoryStorage())
	pre := state.NewTxn(st.NewSnapshot())

	for i := 0; i < 12; i++ {
		pre.AddBalance(addrs[i], big.NewInt(int64(1000*(i%3))))
		pre.SetNonce(addrs[i], uint64(i%2))

		if i%4 == 0 {
			pre.SetCode(addrs[i], []byte{0x60, byte(i)})
			pre.SetState(addrs[i], types.Hash{1}, types.Hash{byte(i + 1)})
		}
	}

	objs, err := pre.Commit(true)
	require.NoError(t, err)

	snap, _, err := st.NewSnapshot().Commit(objs)
	require.NoError(t, err)

	for seed := int64(0); seed < 300; seed++ {
		newer := state.NewTxn(snap)
		ref := state.NewTxn(snap)

		r := rand.New(rand.NewSource(seed))
		steps := 20 + r.Intn(120)

		for s := 0; s < steps; s++ {
			a := addrs[r.Intn(len(addrs))]
			op := r.Intn(12)

			for _, txn := range []*state.Txn{newer, ref} {
				rr := rand.New(rand.NewSource(seed*1000 + int64(s))) // same args for both

				switch op {
				case 0:
					txn.AddBalance(a, big.NewInt(int64(rr.Intn(3))))
				case 1:
					_ = txn.SubBalance(a, big.NewInt(int64(rr.Intn(2))))
				case 2:
					txn.SetNonce(a, uint64(rr.Intn(2)))
				case 3:
					txn.SetCode(a, []byte{byte(rr.Intn(3))})
				case 4:
					txn.SetState(a, types.Hash{byte(rr.Intn(3))}, types.Hash{byte(rr.Intn(3))})
				case 5:
					txn.Suicide(a)
				case 6:
					txn.TouchAccount(a)
				case 7:
					txn.CreateAccount(a)
				case 8:
					txn.SetBalance(a, big.NewInt(0))
				case 9:
					// A call that writes and is then reverted.
					id := txn.Snapshot()
					txn.AddBalance(a, big.NewInt(5))
					txn.TouchAccount(addrs[(rr.Intn(len(addrs)))])
					require.NoError(t, txn.RevertToSnapshot(id))
				}
			}

			if op >= 10 { // end of a transaction
				require.NoError(t, newer.CleanDeleteObjects(true))
				refCleanDeleteObjects(ref, true)
			}
		}

		refCleanDeleteObjects(ref, true)

		gotNew, err := newer.Commit(true)
		require.NoError(t, err)

		gotRef, err := ref.Commit(true)
		require.NoError(t, err)

		require.Equal(t, normalize(gotRef), normalize(gotNew), "seed %d", seed)
	}
}

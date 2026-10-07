package itrie

// Audit review L1 regression test.

import (
	"math/big"
	"sync"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/state"
	"github.com/w-chain-team/node/types"
)

// Nodes decoded from storage are now shared through the global decodedNodes
// cache. Txn.delete clears ShortNode.hash in place and the hasher writes it
// back, so two commits from the same parent (block import, proposal
// verification, a second chain view) mutate the same cached node at once.
// Run with -race. Also checks the roots stay right.
func TestDecodedNodeCache_ConcurrentCommits(t *testing.T) {
	addr := func(i int) types.Address { return types.BytesToAddress(big.NewInt(int64(i) + 7000).Bytes()) }

	st, err := NewLevelDBStorage(t.TempDir(), hclog.NewNullLogger())
	require.NoError(t, err)

	objs := make([]*state.Object, 0, 2000)
	for i := 0; i < 2000; i++ {
		objs = append(objs, &state.Object{Address: addr(i), Balance: big.NewInt(int64(i + 1)),
			Root: emptyStateHash, CodeHash: types.EmptyCodeHash})
	}

	_, root, err := NewState(st).NewSnapshot().Commit(objs) //nolint
	require.NoError(t, err)

	change := func(g int) []*state.Object {
		out := []*state.Object{}
		for i := g; i < 2000; i += 7 {
			out = append(out, &state.Object{Address: addr(i), Deleted: true})
		}

		return out
	}

	want := make([]types.Hash, 6)

	for g := 0; g < 6; g++ {
		snap, err := NewState(st).NewSnapshotAt(types.BytesToHash(root))
		require.NoError(t, err)

		_, r, err := snap.Commit(change(g))
		require.NoError(t, err)

		want[g] = types.BytesToHash(r)
	}

	for iter := 0; iter < 20; iter++ {
		var wg sync.WaitGroup

		got := make([]types.Hash, 6)

		for g := 0; g < 6; g++ {
			wg.Add(1)

			go func(g int) {
				defer wg.Done()

				snap, err := NewState(st).NewSnapshotAt(types.BytesToHash(root))
				if err != nil {
					t.Error(err)

					return
				}

				_, r, err := snap.Commit(change(g))
				if err != nil {
					t.Error(err)

					return
				}

				got[g] = types.BytesToHash(r)
			}(g)
		}

		wg.Wait()
		require.Equal(t, want, got, "iteration %d", iter)
	}
}

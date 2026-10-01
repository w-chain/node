package itrie

import (
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/state"
	"github.com/w-chain-team/node/types"
)

// ST-M2: block import, RPC and the txpool read the same cached trie at once.
// Lookups used to write resolved nodes back into it, a data race. Run with
// -race; every read must also return the right account.
func TestTrieLookup_ConcurrentReadersOnSharedTrie(t *testing.T) {
	t.Parallel()

	addr := func(i int) types.Address { return types.BytesToAddress(big.NewInt(int64(i) + 5000).Bytes()) }

	st := NewMemoryStorage()

	objs := make([]*state.Object, 0, 3000)
	for i := 0; i < 3000; i++ {
		objs = append(objs, &state.Object{Address: addr(i), Balance: big.NewInt(int64(i + 1)),
			Root: emptyStateHash, CodeHash: types.EmptyCodeHash})
	}

	_, root, err := NewState(st).NewSnapshot().Commit(objs)
	require.NoError(t, err)

	// A fresh process loads the head from disk, then one commit puts a trie
	// with unresolved hash references into the shared cache.
	s := NewState(st)
	parent, err := s.NewSnapshotAt(types.BytesToHash(root))
	require.NoError(t, err)

	_, root2, err := parent.Commit([]*state.Object{{Address: addr(0), Balance: big.NewInt(7),
		Root: emptyStateHash, CodeHash: types.EmptyCodeHash}})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for g := 0; g < 6; g++ {
		wg.Add(1)

		go func(g int) {
			defer wg.Done()

			snap, err := s.NewSnapshotAt(types.BytesToHash(root2))
			if err != nil {
				t.Error(err)

				return
			}

			for i := 0; i < 3000; i++ {
				j := (i + g*500) % 3000

				a, err := snap.GetAccount(addr(j))
				if err != nil || a == nil {
					t.Errorf("account %d missing", j)

					return
				}

				want := int64(j + 1)
				if j == 0 {
					want = 7
				}

				if a.Balance.Int64() != want {
					t.Errorf("account %d: balance %d, want %d", j, a.Balance.Int64(), want)

					return
				}
			}
		}(g)
	}

	wg.Wait()
}

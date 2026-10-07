package syncer

// Mainnet epoch freezes (2026-10-06/07), part B: Sync's skip list was cleared only when EVERY
// peer was skipped. With one connected peer not ahead, BestPeer kept returning it, the loop
// continued silently, and an up-to-date peer skipped once was never asked again: the syncer
// could not rescue a validator stuck in consensus. Regression test from the investigation's
// repro (wchain-notes/node8-freeze).

import (
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/w-chain-team/node/blockchain"
	"github.com/w-chain-team/node/types"
)

func runSkipTrap(t *testing.T, withLaggingPeer bool) (aCalls int32, finalHead uint64) {
	t.Helper()
	var mu sync.Mutex
	head := uint64(1000)
	getHead := func() uint64 {
		mu.Lock()
		defer mu.Unlock()

		return head
	}

	bc := &mockBlockchain{
		subscription:  blockchain.NewMockSubscription(),
		headerHandler: func() *types.Header { return &types.Header{Number: getHead()} },
		verifyFinalizedBlockHandler: func(b *types.Block) (*types.FullBlock, error) {
			return &types.FullBlock{Block: b}, nil
		},
		writeFullBlockHandler: func(fb *types.FullBlock) error {
			mu.Lock()
			defer mu.Unlock()
			if fb.Block.Number() == head+1 {
				head++
			}
			return nil
		},
	}

	var calls int32
	var aNumber atomic.Uint64
	aNumber.Store(1001)
	client := &mockSyncPeerClient{
		getBlocksHandler: func(id peer.ID, from uint64, _ time.Duration) (<-chan *types.Block, error) {
			n := atomic.AddInt32(&calls, 1)
			ch := make(chan *types.Block, 64)
			if n > 1 { // 1st download from A comes up short (stream ends with no blocks, no error)
				for b := from; b <= aNumber.Load(); b++ {
					ch <- &types.Block{Header: &types.Header{Number: b}}
				}
			}
			close(ch)
			return ch, nil
		},
	}
	s := NewTestSyncer(nil, bc, time.Second, client, &mockProgression{})

	A := &NoForkPeer{ID: peer.ID("A-uptodate"), Number: 1001, Distance: big.NewInt(1)}
	s.peerMap.Put(A)
	if withLaggingPeer {
		s.peerMap.Put(&NoForkPeer{ID: peer.ID("L-lagging"), Number: 990, Distance: big.NewInt(2)})
	}
	go func() { _ = s.Sync(func(*types.FullBlock) bool { return false }) }()

	// A keeps announcing new blocks (as gossip status updates do every 2 s on mainnet)
	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Millisecond)
		n := uint64(1001 + i)
		aNumber.Store(n)
		s.putToPeerMap(&NoForkPeer{ID: A.ID, Number: n, Distance: big.NewInt(1)})
	}
	time.Sleep(50 * time.Millisecond)
	return atomic.LoadInt32(&calls), getHead()
}

func TestSync_SkippedPeerRetriedWhenNoOtherPeerIsAhead(t *testing.T) {
	calls, head := runSkipTrap(t, true)
	t.Logf("WITH one lagging peer:    download attempts=%d, local head 1000 -> %d (peer A is at 1060)", calls, head)
	if calls < 2 || head <= 1001 {
		t.Fatalf("syncer stayed trapped: calls=%d head=%d", calls, head)
	}

	calls, head = runSkipTrap(t, false)
	t.Logf("WITHOUT a lagging peer:  download attempts=%d, local head 1000 -> %d", calls, head)
	if head <= 1000 {
		t.Fatalf("control: expected the syncer to recover, head=%d", head)
	}
}

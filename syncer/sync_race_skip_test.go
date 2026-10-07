package syncer

// Mainnet epoch freezes, part B: the silent way an UP-TO-DATE peer landed on the skip list.
// Sync() line 186 sees peer at N, local at N-1 -> "peer ahead". Before bulkSyncWithPeer re-reads
// the local head (line 214) consensus writes N, so it requests N+1. The peer's server
// (service.go GetBlocks: `for i := From; i <= Header().Number`) sends nothing and ends the stream
// normally -> client gets 0 blocks, err=nil -> lastNumber(0) < N -> skipList[peer]=true, no log.

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

func TestSync_PeerNotSkippedWhenConsensusWroteItsBlock(t *testing.T) {
	var mu sync.Mutex
	local := uint64(1000)
	headerCalls := 0
	peerHead := uint64(1001) // peer A has block 1001, announces it

	bc := &mockBlockchain{
		subscription: blockchain.NewMockSubscription(),
		headerHandler: func() *types.Header {
			mu.Lock()
			defer mu.Unlock()
			headerCalls++
			// reads: 1=Sync start, 2=loop check (line 172), 3=bulkSyncWithPeer re-read (line 214)
			if headerCalls == 3 { // consensus wrote 1001 between the check and the request
				local = 1001
			}
			return &types.Header{Number: local}
		},
		verifyFinalizedBlockHandler: func(b *types.Block) (*types.FullBlock, error) { return &types.FullBlock{Block: b}, nil },
		writeFullBlockHandler:       func(*types.FullBlock) error { return nil },
	}

	var requestedFrom atomic.Uint64
	var calls int32
	client := &mockSyncPeerClient{
		// faithful to service.go GetBlocks: send From..peerHead, then end the stream
		getBlocksHandler: func(_ peer.ID, from uint64, _ time.Duration) (<-chan *types.Block, error) {
			atomic.AddInt32(&calls, 1)
			requestedFrom.Store(from)
			mu.Lock()
			top := peerHead
			mu.Unlock()

			ch := make(chan *types.Block, 64)
			for b := from; b <= top; b++ {
				ch <- &types.Block{Header: &types.Header{Number: b}}
			}
			close(ch)
			return ch, nil
		},
	}
	s := NewTestSyncer(nil, bc, time.Second, client, &mockProgression{})
	A := &NoForkPeer{ID: peer.ID("A-uptodate"), Number: 1001, Distance: big.NewInt(1)}
	s.peerMap.Put(A)
	s.peerMap.Put(&NoForkPeer{ID: peer.ID("L-lagging"), Number: 990, Distance: big.NewInt(2)}) // any slow peer

	go func() { _ = s.Sync(func(*types.FullBlock) bool { return false }) }()
	time.Sleep(10 * time.Millisecond)
	s.notifyNewStatusEvent() // status for 1001 arrives
	time.Sleep(20 * time.Millisecond)
	t.Logf("peer A at %d, Sync saw local=1000; bulkSync re-read local=1001 and requested from %d -> peer had nothing to send",
		peerHead, requestedFrom.Load())

	// From now on A keeps advancing and announcing; is it ever asked again?
	for i := uint64(1); i <= 30; i++ {
		mu.Lock()
		peerHead = 1001 + i
		mu.Unlock()
		s.putToPeerMap(&NoForkPeer{ID: A.ID, Number: 1001 + i, Distance: big.NewInt(1)})
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	t.Logf("after A announced up to 1031: download attempts=%d", atomic.LoadInt32(&calls))
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("A was skipped after consensus wrote its block, attempts=%d", calls)
	}
}

func TestPeerFellShort(t *testing.T) {
	t.Parallel()

	if peerFellShort(&types.Header{Number: 1001}, 1001) {
		t.Fatal("local head reached the announced height: the peer is not to blame")
	}

	if peerFellShort(&types.Header{Number: 1005}, 1001) {
		t.Fatal("local head passed the announced height: the peer is not to blame")
	}

	if !peerFellShort(&types.Header{Number: 1000}, 1001) {
		t.Fatal("local head still below what the peer announced: the peer fell short")
	}
}

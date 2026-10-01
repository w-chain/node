package syncer

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

func newRateTestSyncer(blockCh func() <-chan *types.Block, window time.Duration) (*syncer, *[]*types.Block) {
	synced := []*types.Block{}

	s := NewTestSyncer(
		nil,
		&mockBlockchain{
			headerHandler: newSimpleHeaderHandler(0),
			verifyFinalizedBlockHandler: func(b *types.Block) (*types.FullBlock, error) {
				return &types.FullBlock{Block: b}, nil
			},
			writeFullBlockHandler: func(b *types.FullBlock) error {
				synced = append(synced, b.Block)

				return nil
			},
		},
		5*time.Second, // each block arrives well inside the per-block timeout
		&mockSyncPeerClient{
			getBlocksHandler: func(peer.ID, uint64, time.Duration) (<-chan *types.Block, error) {
				return blockCh(), nil
			},
		},
		&mockProgression{},
	)
	s.syncRateWindow = window

	return s, &synced
}

// S-M1: a peer that claims a far-away head but drips blocks just under the
// per-block timeout is dropped once it falls below the minimum rate, keeping
// the blocks already written.
func TestBulkSync_DropsSlowDripPeer(t *testing.T) {
	t.Parallel()

	blocks := createMockBlocks(1000)

	s, synced := newRateTestSyncer(func() <-chan *types.Block {
		return blocksToCh(blocks, 150*time.Millisecond) // ~6 blocks per second-long window
	}, time.Second)

	start := time.Now()
	last, _, err := s.bulkSyncWithPeer(peer.ID("slow"), 1_000_000, func(*types.FullBlock) bool { return false })

	require.ErrorIs(t, err, errSlowSyncPeer)
	require.Less(t, time.Since(start), 3*time.Second)
	require.NotEmpty(t, *synced, "blocks received before the drop are kept")
	require.Equal(t, (*synced)[len(*synced)-1].Number(), last)
}

// An honest peer streams the blocks it has at full speed and is never dropped.
func TestBulkSync_KeepsFastPeer(t *testing.T) {
	t.Parallel()

	blocks := createMockBlocks(300)

	s, synced := newRateTestSyncer(func() <-chan *types.Block {
		return blocksToCh(blocks, time.Millisecond)
	}, 100*time.Millisecond)

	last, _, err := s.bulkSyncWithPeer(peer.ID("fast"), 300, func(*types.FullBlock) bool { return false })

	require.NoError(t, err)
	require.Len(t, *synced, 300)
	require.Equal(t, uint64(300), last)
}

// Slow execution on this side (heavy blocks) is not the peer's fault: an
// honest peer that streams instantly is kept even if writing each block takes
// longer than the rate allows.
func TestBulkSync_KeepsFastPeerWhenLocalWriteIsSlow(t *testing.T) {
	t.Parallel()

	blocks := createMockBlocks(30)

	s, synced := newRateTestSyncer(func() <-chan *types.Block {
		return blocksToCh(blocks, 0)
	}, 200*time.Millisecond)

	bc, ok := s.blockchain.(*mockBlockchain)
	require.True(t, ok)

	write := bc.writeFullBlockHandler
	bc.writeFullBlockHandler = func(b *types.FullBlock) error {
		time.Sleep(50 * time.Millisecond) // 30 blocks take 1.5s: well over the window at 1 block per 50ms

		return write(b)
	}

	_, _, err := s.bulkSyncWithPeer(peer.ID("fast"), 30, func(*types.FullBlock) bool { return false })

	require.NoError(t, err)
	require.Len(t, *synced, 30)
}

// SYN-M1: the minimum rate used to be 10 blocks per 30 s window (0.33 blocks/s),
// slower than the chain's own 0.5 blocks/s, so a peer dripping just above it
// was never dropped and the node fell further behind forever. Scaled: window
// 300 ms, one block every 29 ms (prod-equivalent one per 2.9 s).
func TestBulkSync_DropsPeerSlowerThanTheChain(t *testing.T) {
	t.Parallel()

	blocks := createMockBlocks(120)

	s, _ := newRateTestSyncer(func() <-chan *types.Block {
		return blocksToCh(blocks, 29*time.Millisecond)
	}, 300*time.Millisecond)

	_, _, err := s.bulkSyncWithPeer(peer.ID("drip"), 1_000_000, func(*types.FullBlock) bool { return false })
	require.ErrorIs(t, err, errSlowSyncPeer)
}

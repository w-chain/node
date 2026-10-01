package syncer

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/w-chain-team/node/syncer/proto"
	"github.com/w-chain-team/node/types"
)

// SYN-H1: block streams served at once are capped; past the cap a request is
// refused straight away instead of starting another chain walk.
func TestGetBlocks_ConcurrentStreamCap(t *testing.T) {
	for i := 0; i < maxConcurrentGetBlocks; i++ {
		getBlocksSlots <- struct{}{}
	}

	defer func() {
		for i := 0; i < maxConcurrentGetBlocks; i++ {
			<-getBlocksSlots
		}
	}()

	svc := &syncPeerService{}
	err := svc.GetBlocks(&proto.GetBlocksRequest{From: 1}, nil)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
}

// SYN-H2: a peer replaying old canonical blocks is dropped at once. Before,
// each one verified, wrote nothing and re-ran the new-block callback, so the
// node never caught up while that peer stayed connected.
func TestBulkSync_RefusesOutOfSequenceBlocks(t *testing.T) {
	t.Parallel()

	written, callbacks := 0, 0

	s := NewTestSyncer(
		nil,
		&mockBlockchain{
			headerHandler: newSimpleHeaderHandler(1000),
			verifyFinalizedBlockHandler: func(b *types.Block) (*types.FullBlock, error) {
				return &types.FullBlock{Block: b}, nil
			},
			writeFullBlockHandler: func(*types.FullBlock) error {
				written++

				return nil
			},
		},
		time.Second,
		&mockSyncPeerClient{
			getBlocksHandler: func(peer.ID, uint64, time.Duration) (<-chan *types.Block, error) {
				return blocksToCh(createMockBlocks(50), 0), nil // blocks 1..50, below our head
			},
		},
		&mockProgression{},
	)

	_, _, err := s.bulkSyncWithPeer(peer.ID("replayer"), 2000, func(*types.FullBlock) bool {
		callbacks++

		return false
	})

	require.ErrorIs(t, err, errUnexpectedSyncBlock)
	require.Zero(t, written)
	require.Zero(t, callbacks)
}

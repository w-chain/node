package syncer

import (
	"errors"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/blockchain"
	"github.com/w-chain-team/node/network"
	"github.com/w-chain-team/node/types"
)

// S-H1: a peer serving a block that fails verification, then more blocks,
// used to leave two pipeline goroutines blocked forever per attempt, each
// holding a block. Closing the stream must now stop them.
func TestBulkSync_NoGoroutineLeakOnBadBlock(t *testing.T) {
	clientSrv := newTestNetwork(t)
	client := newTestSyncPeerClient(clientSrv, nil)

	extra := make([]byte, 256*1024)

	_, peerSrv := createTestSyncerService(t, &mockBlockchain{
		headerHandler: newSimpleHeaderHandler(1_000_000),
		getBlockByNumberHandler: func(u uint64, _ bool) (*types.Block, bool) {
			return &types.Block{Header: &types.Header{Number: u, ExtraData: extra}}, true
		},
	})

	require.NoError(t, network.JoinAndWait(clientSrv, peerSrv,
		network.DefaultBufferTimeout, network.DefaultJoinTimeout))

	s := &syncer{
		logger: client.logger,
		blockchain: &mockBlockchain{
			subscription:  blockchain.NewMockSubscription(),
			headerHandler: newSimpleHeaderHandler(0),
			verifyFinalizedBlockHandler: func(*types.Block) (*types.FullBlock, error) {
				time.Sleep(20 * time.Millisecond)

				return nil, errors.New("bad block")
			},
		},
		syncProgression: &mockProgression{},
		syncPeerClient:  client,
		blockTimeout:    3 * time.Second,
		peerMap:         new(PeerMap),
	}

	time.Sleep(200 * time.Millisecond)

	before := runtime.NumGoroutine()

	const attempts = 20
	for i := 0; i < attempts; i++ {
		_, _, err := s.bulkSyncWithPeer(peerSrv.AddrInfo().ID, math.MaxUint64, func(*types.FullBlock) bool { return false })
		require.Error(t, err)
	}

	// Before the fix this grew by ~2 per attempt and never came back down.
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine()-before < attempts/2
	}, 5*time.Second, 100*time.Millisecond, "pipeline goroutines must exit once the stream is closed")
}

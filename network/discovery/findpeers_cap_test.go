package discovery

import (
	"context"
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/w-chain-team/node/network/proto"
	networkTesting "github.com/w-chain-team/node/network/testing"
)

// G-L1: we ask a peer for maxDiscoveryPeerReqCount entries, but nothing
// bounded its answer: one reply of tens of thousands of entries every round.
func TestFindPeersCall_CapsReply(t *testing.T) {
	t.Parallel()

	ds, err := newDiscoveryService(func(server *networkTesting.MockNetworkingServer) {
		server.GetMockDiscoveryClient().HookFindPeers(
			func(context.Context, *proto.FindPeersReq, ...grpc.CallOption) (*proto.FindPeersResp, error) {
				nodes := make([]string, 50_000)
				for i := range nodes {
					nodes[i] = fmt.Sprintf("/ip4/10.0.%d.%d/tcp/1478/p2p/x", i/250, i%250)
				}

				return &proto.FindPeersResp{Nodes: nodes}, nil
			},
		)
	})
	require.NoError(t, err)

	nodes, err := ds.findPeersCall(peer.ID("peer"), false)
	require.NoError(t, err)
	require.Len(t, nodes, maxDiscoveryPeerReqCount)
}

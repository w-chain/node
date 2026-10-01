package identity

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	networkTesting "github.com/w-chain-team/node/network/testing"
	"github.com/w-chain-team/node/network/proto"
)

// NET-H1: a peer that connects but never answers the identity Hello used to
// hold its pending slot forever; enough of them cut the node off. The
// handshake now gives up after helloTimeout, so the slot is released.
func TestHandshake_SilentPeerTimesOut(t *testing.T) {
	old := helloTimeout
	helloTimeout = 200 * time.Millisecond

	t.Cleanup(func() { helloTimeout = old })

	service := newIdentityService(func(server *networkTesting.MockNetworkingServer) {
		server.GetMockIdentityClient().HookHello(
			func(ctx context.Context, _ *proto.Status, _ ...grpc.CallOption) (*proto.Status, error) {
				<-ctx.Done() // never answers on its own

				return nil, ctx.Err()
			},
		)
	})

	start := time.Now()
	err := service.handleConnected("silent-peer", network.DirInbound)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)
}

package network

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	testproto "github.com/w-chain-team/node/network/proto"
)

// NET-C1: a gossip message over pubsub's 1 MiB default was silently dropped
// by the sender, so a block proposal over that size never reached anyone.
func TestGossip_DeliversMessagesOverOneMiB(t *testing.T) {
	servers, err := createServers(2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { closeTestServers(t, servers) })
	require.Empty(t, MeshJoin(servers...))

	got := make(chan int, 4)
	topics := make([]*Topic, 2)

	for i := 0; i < 2; i++ {
		tp, err := servers[i].NewTopic("size", &testproto.GenericMessage{})
		require.NoError(t, err)

		topics[i] = tp
		i := i

		require.NoError(t, tp.Subscribe(func(obj interface{}, _ peer.ID) {
			if i == 1 {
				got <- len(obj.(*testproto.GenericMessage).Message)
			}
		}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, WaitForSubscribers(ctx, servers[0], "size", 1))

	for _, size := range []int{1100 << 10, 4 << 20} {
		require.NoError(t, topics[0].Publish(&testproto.GenericMessage{Message: strings.Repeat("a", size)}))

		select {
		case n := <-got:
			require.Equal(t, size, n)
		case <-time.After(10 * time.Second):
			t.Fatalf("%d-byte message was not delivered", size)
		}
	}
}

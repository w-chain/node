package syncer

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	gproto "google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/network"
	"github.com/w-chain-team/node/syncer/proto"
)

func TestIsValidStatusMessage(t *testing.T) {
	t.Parallel()

	for _, n := range []uint64{0, 1, 24_400_000, ^uint64(0)} {
		raw, err := gproto.Marshal(&proto.SyncPeerStatus{Number: n})
		require.NoError(t, err)
		require.True(t, isValidStatusMessage(raw), "status %d", n)
	}

	require.False(t, isValidStatusMessage(bytes.Repeat([]byte{'x'}, 15<<20)), "junk")
	require.False(t, isValidStatusMessage([]byte{0xff, 0xff}), "malformed")

	raw, _ := gproto.Marshal(&proto.SyncPeerStatus{Number: 5})
	raw = protowire.AppendVarint(protowire.AppendTag(raw, 9, protowire.VarintType), 1)
	require.False(t, isValidStatusMessage(raw), "unknown field")
}

// Audit review M2: A -- B -- C, A and C not connected. With the validator
// registered, A's 15 MiB junk on the status topic is not relayed by B, while a
// real status still is.
func TestStatusTopicDoesNotRelayJunk(t *testing.T) {
	noDiscover := func(c *network.Config) { c.NoDiscover = true }

	servers := make([]*network.Server, 3)

	for i := range servers {
		s, err := network.CreateServer(&network.CreateServerParams{ConfigCallback: noDiscover})
		require.NoError(t, err)

		servers[i] = s
		t.Cleanup(func() { _ = s.Close() })
	}

	require.NoError(t, network.JoinAndWait(servers[0], servers[1], network.DefaultBufferTimeout, network.DefaultJoinTimeout))
	require.NoError(t, network.JoinAndWait(servers[1], servers[2], network.DefaultBufferTimeout, network.DefaultJoinTimeout))

	got := make(chan uint64, 4)
	topics := make([]*network.Topic, 3)

	for i, s := range servers {
		// A is the attacker: it runs no validator. B and C run this build.
		if i > 0 {
			require.NoError(t, s.RegisterTopicValidator(statusTopicName, isValidStatusMessage))
		}

		tp, err := s.NewTopic(statusTopicName, &proto.SyncPeerStatus{})
		require.NoError(t, err)

		topics[i] = tp
		i := i

		require.NoError(t, tp.Subscribe(func(obj interface{}, _ peer.ID) {
			if i == 2 {
				got <- obj.(*proto.SyncPeerStatus).Number
			}
		}))
	}

	time.Sleep(3 * time.Second) // let the gossipsub mesh form

	// Junk: a valid status followed by 15 MiB of an unknown field.
	raw, _ := gproto.Marshal(&proto.SyncPeerStatus{Number: 7})
	raw = protowire.AppendBytes(protowire.AppendTag(raw, 9, protowire.BytesType), bytes.Repeat([]byte{'x'}, 15<<20))

	junk := &proto.SyncPeerStatus{}
	require.NoError(t, gproto.Unmarshal(raw, junk))
	require.NoError(t, topics[0].Publish(junk))

	require.NoError(t, topics[0].Publish(&proto.SyncPeerStatus{Number: 42}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	select {
	case n := <-got:
		require.Equal(t, uint64(42), n, "only the real status arrives")
	case <-ctx.Done():
		t.Fatal("real status not relayed")
	}

	select {
	case n := <-got:
		t.Fatalf("unexpected relayed status %d", n)
	case <-time.After(2 * time.Second):
	}
}

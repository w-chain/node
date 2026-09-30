package network

import (
	"testing"

	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
)

// N-M1: the node listens on TCP only and must also dial TCP only. libp2p's
// default transports made it dial peer-advertised QUIC and WebTransport
// addresses, reaching known webtransport-go / quic-go DoS bugs.
func TestServer_DialsTCPOnly(t *testing.T) {
	t.Parallel()

	srv, err := CreateServer(nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	sw, ok := srv.host.Network().(*swarm.Swarm)
	require.True(t, ok)

	for addr, dialable := range map[string]bool{
		"/ip4/1.2.3.4/tcp/1478":                      true,
		"/ip4/1.2.3.4/udp/4001/quic-v1":              false,
		"/ip4/1.2.3.4/udp/4001/quic-v1/webtransport": false,
		"/ip4/1.2.3.4/tcp/4001/ws":                   false,
	} {
		ma, err := multiaddr.NewMultiaddr(addr)
		require.NoError(t, err)

		require.Equal(t, dialable, sw.TransportForDialing(ma) != nil, addr)
	}
}

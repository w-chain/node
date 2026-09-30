package discovery

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
)

func mustAddrs(t *testing.T, ss ...string) []multiaddr.Multiaddr {
	t.Helper()

	out := make([]multiaddr.Multiaddr, 0, len(ss))

	for _, s := range ss {
		a, err := multiaddr.NewMultiaddr(s)
		require.NoError(t, err)

		out = append(out, a)
	}

	return out
}

// P2-M3: a public peer cannot make us dial internal addresses.
func TestPublicAddrs(t *testing.T) {
	t.Parallel()

	in := mustAddrs(t,
		"/ip4/84.247.166.57/tcp/1478",  // public: kept
		"/dns4/node.example.com/tcp/1", // DNS: kept, resolves on dial
		"/ip4/10.0.0.5/tcp/1478",       // private
		"/ip4/192.168.1.2/tcp/1478",    // private
		"/ip4/172.17.0.1/tcp/1478",     // private (docker)
		"/ip4/127.0.0.1/tcp/1478",      // loopback
		"/ip4/169.254.169.254/tcp/80",  // link-local (cloud metadata)
		"/ip4/0.0.0.0/tcp/1478",        // unspecified
		"/ip6/::1/tcp/1478",            // loopback
		"/ip6/fe80::1/tcp/1478",        // link-local
	)

	require.Equal(t, in[:2], publicAddrs(in))
}

// A peer on a private network (LAN, local test chain) may share private
// addresses; a public one may not.
func TestIsPublicSource(t *testing.T) {
	t.Parallel()

	require.True(t, isPublicSource(&peer.AddrInfo{Addrs: mustAddrs(t, "/ip4/84.247.166.57/tcp/1478")}))
	require.False(t, isPublicSource(&peer.AddrInfo{Addrs: mustAddrs(t, "/ip4/127.0.0.1/tcp/30301")}))
	require.False(t, isPublicSource(&peer.AddrInfo{Addrs: mustAddrs(t, "/ip4/10.0.0.5/tcp/1478")}))
	require.True(t, isPublicSource(nil), "unknown source is treated strictly")
}

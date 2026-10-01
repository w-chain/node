package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// RPC-L3: on IBFT the hub embedded a nil bridge provider, so every bridge_*
// call panicked (recovered, but a stack trace per call and failed batches).
func TestNoBridgeProvider_ReturnsError(t *testing.T) {
	t.Parallel()

	p := noBridgeProvider{}

	require.NotPanics(t, func() {
		_, err := p.GenerateExitProof(1)
		require.ErrorIs(t, err, errBridgeNotSupported)

		_, err = p.GetStateSyncProof(1)
		require.ErrorIs(t, err, errBridgeNotSupported)
	})
}

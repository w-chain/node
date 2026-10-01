package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// G-M1: Export streams the chain from block 0 and needs no authentication;
// any number could run at once. A second one now fails fast.
func TestExport_OneAtATime(t *testing.T) {
	require.True(t, exportRunning.CompareAndSwap(false, true), "simulate an export in progress")
	defer exportRunning.Store(false)

	err := (&systemService{}).Export(nil, nil)
	require.ErrorIs(t, err, errExportRunning)
}

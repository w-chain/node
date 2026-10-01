package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteSyncThrottle(t *testing.T) {
	var nilThrottle *WriteSyncThrottle
	require.False(t, nilThrottle.ShouldSync())

	th := &WriteSyncThrottle{}
	require.True(t, th.ShouldSync(), "first write is flushed")
	require.False(t, th.ShouldSync(), "a second write within the interval is not")

	th.last.Store(time.Now().Add(-WriteSyncInterval).UnixNano())
	require.True(t, th.ShouldSync(), "flushed again once the interval passed")
}

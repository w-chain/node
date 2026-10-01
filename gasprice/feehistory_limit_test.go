package gasprice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// RPC-H1: every block in the range got one reward entry per requested
// percentile, so a request listing millions of percentiles allocated
// gigabytes. The list is capped at 100, like geth.
func TestFeeHistory_PercentileCap(t *testing.T) {
	t.Parallel()

	gasHelper, err := NewGasHelper(DefaultGasHelperConfig, createTestBlocks(t, 30))
	require.NoError(t, err)

	percentiles := func(n int) []float64 {
		p := make([]float64, n)
		for i := range p {
			p[i] = float64(i) * 100 / float64(n)
		}

		return p
	}

	_, err = gasHelper.FeeHistory(10, 30, percentiles(maxRewardPercentiles))
	require.NoError(t, err, "the limit itself is allowed")

	_, err = gasHelper.FeeHistory(10, 30, percentiles(maxRewardPercentiles+1))
	require.ErrorIs(t, err, ErrTooManyPercentiles)
}

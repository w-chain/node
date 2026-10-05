package ibft

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

// BI-H1: the nonce is not hashed and IBFT always leaves it empty.
func TestVerifyV110Header_Nonce(t *testing.T) {
	t.Parallel()

	require.NoError(t, verifyV110Header(&types.Header{}))
	require.ErrorIs(t, verifyV110Header(&types.Header{Nonce: types.Nonce{1}}), errInvalidNonce)
}

// IB-M1: the parent's seals were committed at a round other than the one in
// our copy of the parent.
func TestVerifyParentSealsAtAnyRound(t *testing.T) {
	t.Parallel()

	errSeals := errors.New("invalid seals")

	committedAt := func(want uint64) (func(*uint64) error, *[]uint64) {
		tried := []uint64{}

		return func(round *uint64) error {
			if round != nil {
				tried = append(tried, *round)
			}

			if round != nil && *round == want {
				return nil
			}

			return errSeals
		}, &tried
	}

	local := uint64(0)

	// Seals from round 1, our copy says round 0.
	verify, _ := committedAt(1)
	require.ErrorIs(t, verifyParentSealsAtAnyRound(&local, false, verify), errSeals, "before WChainV110")

	verify, _ = committedAt(1)
	require.NoError(t, verifyParentSealsAtAnyRound(&local, true, verify), "from WChainV110")

	// Rounds beyond the message window are never tried.
	verify, tried := committedAt(maxParentSealRound + 1)
	require.ErrorIs(t, verifyParentSealsAtAnyRound(&local, true, verify), errSeals)
	require.Len(t, *tried, int(maxParentSealRound)+1)

	// Matching round: no search.
	verify, tried = committedAt(0)
	require.NoError(t, verifyParentSealsAtAnyRound(&local, true, verify))
	require.Len(t, *tried, 1)

	// Legacy hash (no round in the parent): unchanged.
	verify, _ = committedAt(0)
	require.ErrorIs(t, verifyParentSealsAtAnyRound(nil, true, verify), errSeals)
}

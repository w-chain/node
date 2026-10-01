package ibft

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
	"github.com/w-chain-team/node/validators"
)

// Audit N1: the set for an epoch's first block E needs the state after E-1. A
// validator still executing E-1 (head E-2) must not drop the proposal for E.
func TestValidatorsForMessage_EpochBoundary(t *testing.T) {
	const epoch = uint64(20)

	oldSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1")),
		validators.NewECDSAValidator(types.StringToAddress("0x2")),
	)

	// Mirrors the contract store: the set for height h is read at the block
	// before h's epoch start, and only blocks up to head have state.
	getter := func(head uint64) func(uint64) (validators.Validators, error) {
		return func(height uint64) (validators.Validators, error) {
			fetch := (height/epoch)*epoch - 1
			if fetch > head {
				return nil, fmt.Errorf("header not found at %d", fetch)
			}

			return oldSet, nil
		}
	}

	headAt := func(n uint64) func() uint64 { return func() uint64 { return n } }

	sender := types.StringToAddress("0x1")

	// head E-2, message for E: falls back to the newest set.
	vals, err := validatorsForMessage(40, headAt(38), getter(38))
	require.NoError(t, err)
	require.True(t, vals.Includes(sender))

	// head E-1, message for E: the real set, no fallback needed.
	vals, err = validatorsForMessage(40, headAt(39), getter(39))
	require.NoError(t, err)
	require.True(t, vals.Includes(sender))

	// Further ahead than head+2: still refused.
	_, err = validatorsForMessage(40, headAt(37), getter(37))
	require.Error(t, err)

	// A failure at head+1 is not hidden.
	failing := func(uint64) (validators.Validators, error) { return nil, fmt.Errorf("boom") }
	_, err = validatorsForMessage(39, headAt(38), failing)
	require.Error(t, err)
}

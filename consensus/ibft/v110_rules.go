package ibft

import (
	"errors"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/types"
)

// maxParentSealRound is the highest round parent committed seals are checked
// against when they do not match the round of the local parent copy. It is
// the same bound as the consensus message window: no message for a later
// round is accepted, so no block can be committed at one.
const maxParentSealRound = maxRound

var errInvalidNonce = errors.New("invalid nonce")

// isWChainV110 reports whether the WChainV110 rules apply at number.
func (i *backendIBFT) isWChainV110(number uint64) bool {
	return i.config != nil && i.config.Params != nil && i.config.Params.Forks != nil &&
		i.config.Params.Forks.IsActive(chain.WChainV110, number)
}

// verifyV110Header holds the WChainV110 header rules. The nonce is not part
// of the IBFT block hash and IBFT always leaves it empty; checking it stops a
// peer from serving finalized blocks with a different value (audit BI-H1).
func verifyV110Header(header *types.Header) error {
	if header.Nonce != (types.Nonce{}) {
		return errInvalidNonce
	}

	return nil
}

// verifyParentSealsAtAnyRound checks parent committed seals at the round of
// the local parent copy and, from WChainV110 (anyRound), at every other round
// up to maxParentSealRound.
//
// The seals sign the parent hash together with the round it was committed
// in, and the round is not part of the hash. One block can be committed at
// round r on some validators and at r+1 on others (re-proposed after a round
// change), so the proposer's copy of the parent may carry another round than
// ours, and every child was rejected (audit IB-M1). A quorum of seals for the
// same parent at any round proves the same thing.
func verifyParentSealsAtAnyRound(localRound *uint64, anyRound bool, verifyAt func(round *uint64) error) error {
	err := verifyAt(localRound)
	if err == nil || localRound == nil || !anyRound {
		return err
	}

	for round := uint64(0); round <= maxParentSealRound; round++ {
		if round == *localRound {
			continue
		}

		r := round
		if verifyAt(&r) == nil {
			return nil
		}
	}

	return err
}

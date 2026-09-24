package ibft

import (
	"errors"
	"fmt"
	"time"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/types"
)

// maxFutureBlockTime is how far ahead of this validator's clock a proposed
// block's timestamp may be. Honest proposers stamp at most one block time
// ahead; the rest is headroom for clock skew between validators.
const maxFutureBlockTime = 30 * time.Second

var (
	errTimestampNotAfterParent = errors.New("block timestamp is not after its parent")
	errTimestampTooFarAhead    = errors.New("block timestamp is too far in the future")
	errInvalidBaseFee          = errors.New("invalid base fee")
	errStateTxNotAllowed       = errors.New("state transactions are not allowed")
)

// isWChainV108 reports whether the v1.0.8 consensus rules apply at number.
func (i *backendIBFT) isWChainV108(number uint64) bool {
	return i.config != nil && i.config.Params != nil && i.config.Params.Forks != nil &&
		i.config.Params.Forks.IsActive(chain.WChainV108, number)
}

// verifyV108Header holds the deterministic header rules: every node, whether
// voting on a proposal or syncing a finalized block, reaches the same answer.
func verifyV108Header(parent, header *types.Header, expectedBaseFee uint64) error {
	if header.Timestamp <= parent.Timestamp {
		return errTimestampNotAfterParent
	}

	if header.BaseFee != expectedBaseFee {
		return fmt.Errorf("%w: have %d, want %d", errInvalidBaseFee, header.BaseFee, expectedBaseFee)
	}

	return nil
}

// verifyV108Proposal holds the rules only a voting validator applies. The
// clock check stays out of block import on purpose: a syncing node with a slow
// clock must never refuse a block the validators already finalized.
func verifyV108Proposal(block *types.Block, now time.Time) error {
	if time.Unix(int64(block.Header.Timestamp), 0).After(now.Add(maxFutureBlockTime)) {
		return errTimestampTooFarAhead
	}

	// IBFT never produces state transactions; they skip signature and fee
	// checks, so a proposer must not be able to include one.
	for _, tx := range block.Transactions {
		if tx.Type == types.StateTx {
			return errStateTxNotAllowed
		}
	}

	return nil
}

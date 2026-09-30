package ibft

import (
	"errors"
	"math"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/types"
)

var errTimestampOutOfRange = errors.New("block timestamp is out of range")

// isWChainV109 reports whether the WChainV109 rules apply at number.
func (i *backendIBFT) isWChainV109(number uint64) bool {
	return i.config != nil && i.config.Params != nil && i.config.Params.Forks != nil &&
		i.config.Params.Forks.IsActive(chain.WChainV109, number)
}

// verifyV109Header bounds the header timestamp for every node, voting or
// syncing. A timestamp above MaxInt64 turns negative wherever it is used as
// a signed time, and no child block can ever be stamped after it.
func verifyV109Header(header *types.Header) error {
	if header.Timestamp > math.MaxInt64 {
		return errTimestampOutOfRange
	}

	return nil
}

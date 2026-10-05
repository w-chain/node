package ibft

import "github.com/w-chain-team/node/network"

const (
	// maxBlockBytes is the most block RLP a proposer builds, whatever the
	// validator count. It stays under the 1 MiB gossip limit of nodes not yet
	// upgraded, so a proposal reaches them too during a rolling upgrade, and
	// far under the 4 MiB gRPC message limit the block syncer runs with.
	maxBlockBytes = 896 << 10

	// minBlockBytes is the least a proposer may always build: one pool-sized
	// transaction (128 KB) plus header. Below this the chain would stop
	// including large transactions; above ~63 validators the round-change
	// guarantee below no longer holds and a failed prepared round needs a
	// restart to recover.
	minBlockBytes = 160 << 10

	// blockBytesMargin covers what wraps the block on the wire: committed
	// seals added after building, protobuf framing and the message envelope.
	blockBytesMargin = 256 << 10

	// txBytesFraming is the per-transaction RLP list overhead inside a block.
	txBytesFraming = 4

	// minTxBytes is below the smallest real transaction; with less space
	// left than this nothing more can fit.
	minTxBytes = 96

	// maxGuaranteedValidators is the largest validator set for which one
	// failed prepared round (go-ibft copies the block ~(1+2N) times) still
	// fits the gossip limit. Above it the floor keeps the chain able to
	// include a 128 KB transaction, and the single-failed-round amplification
	// joins the parked go-ibft round-change-size work. =49 at 16 MiB gossip.
	maxGuaranteedValidators = ((network.MaxGossipMessageSize-blockBytesMargin)/minBlockBytes - 1) / 2
)

// compactBytesPerValidator bounds what one validator adds to the largest
// compact consensus message: its stripped round change (sender, signature,
// prepared claim) in a proposal's certificate, and its PREPARE in the one
// full prepared certificate. Measured ~0.4 KB; 1 KB leaves room.
const compactBytesPerValidator = 1 << 10

// blockByteBudget is how many bytes of block RLP a proposer may build when
// validatorCount validators take part. compact is WChainV111: certificates no
// longer copy the block, so the largest message is one block plus a small
// part per validator.
//
// A proposal travels inside one gossip message, so the block must fit the
// gossip limit. go-ibft also copies the prepared block into every round-change
// message twice, and a proposal for round 1+ carries all of them, so after one
// failed prepared round the message is about (1 + 2N) times the block. The
// budget keeps that worst case under the limit for the live validator set, so
// it needs no change as validators join (Node-as-a-Service).
func blockByteBudget(validatorCount int, compact bool) uint64 {
	if validatorCount < 1 {
		validatorCount = 1
	}

	var budget uint64

	if compact {
		overhead := uint64(blockBytesMargin) + uint64(validatorCount)*compactBytesPerValidator
		if overhead < network.MaxGossipMessageSize {
			budget = network.MaxGossipMessageSize - overhead
		}
	} else {
		budget = uint64(network.MaxGossipMessageSize-blockBytesMargin) / uint64(1+2*validatorCount)
	}

	if budget > maxBlockBytes {
		return maxBlockBytes
	}

	if budget < minBlockBytes {
		return minBlockBytes
	}

	return budget
}

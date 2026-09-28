package ibft

import "github.com/0xPolygon/go-ibft/messages/proto"

const (
	// maxFutureHeights is how far past the height being built (head+1) a
	// consensus message may claim to be. Honest messages are for the height
	// the network is building, so a small margin only matters for a node a
	// few blocks behind — and such a node catches up through the syncer, not
	// through stored future messages. Keeping the window far below the epoch
	// size means every accepted height resolves to a cached validator set.
	maxFutureHeights = 16

	// maxRound bounds the round a message may claim. go-ibft's round timeout
	// is base*2^round, so round 20 already means weeks of waiting; no honest
	// message reaches 32.
	maxRound = 32
)

// isWithinMessageWindow reports whether a consensus message's view is close
// enough to this node's head to be worth checking at all.
//
// IsValidValidator does an ecrecover and a validator-set lookup for the height
// a message claims, before go-ibft ever compares that height with its own.
// The validator-set cache holds 3 epochs, so an arbitrary old or far-future
// height forces an EVM call on historical state — per message, on every
// validator the gossip reaches (audit M4). Filtering on the view first makes
// such messages cost almost nothing.
//
// Heights below head are already finished (go-ibft drops them anyway); head
// itself is allowed so late messages for the block just committed stay cheap
// no-ops rather than log noise.
func isWithinMessageWindow(view *proto.View, head uint64) bool {
	if view == nil {
		return false
	}

	return view.Height >= head &&
		view.Height <= head+1+maxFutureHeights &&
		view.Round <= maxRound
}

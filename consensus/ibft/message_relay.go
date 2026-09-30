package ibft

import (
	"sync"

	"github.com/0xPolygon/go-ibft/messages/proto"
	gproto "google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/types"
)

// maxStoredBytesPerSender bounds the consensus messages one validator can
// have held by go-ibft for heights not yet finished. go-ibft keeps one message
// per sender for every height, round and type it is sent, so a single
// validator could fill the height/round window with ~1MiB messages and pin
// ~1GB on every other validator (audit C-M3). An honest validator's worst
// case — a long halt with round changes carrying a full block each round —
// stays around half of this.
const maxStoredBytesPerSender = 64 << 20

// relayCheck decides whether a well-formed consensus message is worth
// processing and relaying. Before, any structurally valid message was relayed
// by every node and cost every validator an ecrecover and an ERROR log line,
// even with a made-up sender (audit C-M1).
//
// The order keeps forged traffic cheap: the height window and the (cached)
// validator-set membership of the claimed sender run before the signature is
// recovered.
func relayCheck(
	msg *proto.Message,
	head uint64,
	isValidatorAt func(addr types.Address, height uint64) bool,
	verifySignature func(*proto.Message) bool,
) bool {
	if validateIBFTMessage(msg) != nil {
		return false
	}

	if !isWithinMessageWindow(msg.View, head) {
		return false
	}

	if !isValidatorAt(types.BytesToAddress(msg.From), msg.View.Height) {
		return false
	}

	return verifySignature(msg)
}

// isRelayableIBFTMessage is the gossip validator for the consensus topic. It
// runs before a message is delivered here or relayed to peers. A node more
// than the window behind the network stops relaying current traffic; it
// cannot use it anyway and catches up through the syncer.
func (i *backendIBFT) isRelayableIBFTMessage(data []byte) bool {
	msg := &proto.Message{}
	if err := gproto.Unmarshal(data, msg); err != nil {
		return false
	}

	return relayCheck(msg, i.blockchain.Header().Number, i.isValidatorAt, i.IsValidValidator)
}

// isValidatorAt reports whether addr is in the validator set for height.
func (i *backendIBFT) isValidatorAt(addr types.Address, height uint64) bool {
	validators, err := i.forkManager.GetValidators(height)
	if err != nil || validators == nil {
		return false
	}

	return validators.Includes(addr)
}

// storedMessageBudget mirrors what go-ibft keeps per sender: messages for
// heights above head, dropped once the chain moves past them.
type storedMessageBudget struct {
	mu   sync.Mutex
	used map[uint64]map[string]int // height -> sender -> bytes
}

// allow records a message about to be handed to go-ibft and reports whether
// its sender is still within budget. from must already be authenticated
// (checked by the gossip validator), so nobody can spend another validator's
// budget.
func (b *storedMessageBudget) allow(from []byte, height, head uint64, size int) bool {
	// go-ibft discards messages for finished heights itself; they cost nothing.
	if height <= head {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.used == nil {
		b.used = make(map[uint64]map[string]int)
	}

	// Forget heights go-ibft has pruned.
	for h := range b.used {
		if h <= head {
			delete(b.used, h)
		}
	}

	sender := string(from)

	total := size
	for _, senders := range b.used {
		total += senders[sender]
	}

	if total > maxStoredBytesPerSender {
		return false
	}

	if b.used[height] == nil {
		b.used[height] = make(map[string]int)
	}

	b.used[height][sender] += size

	return true
}

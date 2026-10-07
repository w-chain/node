package ibft

import (
	"sync"

	"github.com/0xPolygon/go-ibft/messages/proto"
	gproto "google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/types"
	"github.com/w-chain-team/node/validators"
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

	return i.relayable(msg, i.blockchain.Header().Number)
}

// relayable is the gossip validator's decision for a decoded message.
func (i *backendIBFT) relayable(msg *proto.Message, head uint64) bool {
	return relayCheck(msg, head, i.isValidatorAt, i.IsValidValidator) && i.compactUnsignedPartsOK(msg)
}

// isValidatorAt reports whether addr is in the validator set for height.
func (i *backendIBFT) isValidatorAt(addr types.Address, height uint64) bool {
	validators, err := i.messageValidators(height)
	if err != nil || validators == nil {
		return false
	}

	return validators.Includes(addr)
}

// messageValidators returns the validator set used to accept a consensus
// message for height.
func (i *backendIBFT) messageValidators(height uint64) (validators.Validators, error) {
	return validatorsForMessage(height, func() uint64 { return i.blockchain.Header().Number }, i.forkManager.GetValidators)
}

// maxValidatorFallbackDistance is how far above head a message's height may
// be and still be checked against the newest set this node can compute.
const maxValidatorFallbackDistance = 2

// validatorsForMessage returns the validator set for height, or the newest
// set this node can compute when the set for height needs a block it has not
// finished yet.
//
// The set for an epoch's first block E is read from the state after E-1. The
// proposer of E proposes as soon as it has E-1, while other validators may
// still be executing E-1. They used to drop that proposal, and stop relaying
// it, until a round change (audit N1). Falling back only decides whether the
// message is stored and relayed: go-ibft counts votes with the voting power of
// the real set for the height once it starts it, and checks the proposer
// against that set, so a sender outside the real set still has no effect.
func validatorsForMessage(
	height uint64,
	headNumber func() uint64,
	getValidators func(uint64) (validators.Validators, error),
) (validators.Validators, error) {
	vals, err := getValidators(height)
	if err == nil && vals != nil {
		return vals, nil
	}

	head := headNumber()
	if height <= head+1 || height > head+maxValidatorFallbackDistance {
		return vals, err
	}

	return getValidators(head + 1)
}

// storedMessageBudget mirrors what go-ibft keeps per sender: messages for
// heights above head, dropped once the chain moves past them.
type storedMessageBudget struct {
	mu   sync.Mutex
	used map[uint64]map[string]int     // height -> sender -> bytes
	held map[uint64]map[budgetView]int // height -> view -> largest copy charged
}

// budgetView is what go-ibft keeps one message for: sender, round and type.
type budgetView struct {
	sender string
	round  uint64
	typ    proto.MessageType
}

// allow records a message about to be handed to go-ibft and reports whether
// its sender is still within budget. from must already be authenticated
// (checked by the gossip validator).
func (b *storedMessageBudget) allow(from []byte, height, head uint64, size int) bool {
	// go-ibft discards messages for finished heights itself; they cost nothing.
	if height <= head {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	return b.charge(from, height, head, size)
}

// allowMessage is allow for one received message, charged by what go-ibft
// actually holds: one message per sender, height, round and type, so the
// largest copy seen for that view. Another copy of the same view, a replay, a
// re-encoding or a relayer's variant, costs only what it adds over the largest
// one already charged. Charging per copy let anyone use up a validator's budget
// with copies of its own messages and have its real votes dropped (audit
// review C2).
func (b *storedMessageBudget) allowMessage(msg *proto.Message, head uint64) bool {
	height := msg.View.Height
	if height <= head {
		return true
	}

	size := gproto.Size(msg)
	view := budgetView{sender: string(msg.From), round: msg.View.Round, typ: msg.Type}

	b.mu.Lock()
	defer b.mu.Unlock()

	for h := range b.held {
		if h <= head {
			delete(b.held, h)
		}
	}

	prev := b.held[height][view]
	if size <= prev {
		return true
	}

	if !b.charge(msg.From, height, head, size-prev) {
		return false
	}

	if b.held == nil {
		b.held = make(map[uint64]map[budgetView]int)
	}

	if b.held[height] == nil {
		b.held[height] = make(map[budgetView]int)
	}

	b.held[height][view] = size

	return true
}

// charge is allow without the lock.
func (b *storedMessageBudget) charge(from []byte, height, head uint64, size int) bool {
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

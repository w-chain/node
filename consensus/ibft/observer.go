package ibft

// Consensus-message observation for WATCHER nodes.
//
// This file exists only in the watcher build. A normal validator never has an
// observer set, so the whole mechanism costs one nil check per received
// message and changes no behaviour, no message and no timing.
//
// Why it is needed: double-signing is only provable by something that saw BOTH
// conflicting messages. Every node receives them over gossip and then discards
// them within milliseconds, so by the time anyone notices a problem the
// evidence no longer exists anywhere. A watcher keeps them.
//
// The peer ID is captured alongside. It arrives in the same callback and the
// unpatched code throws it away, yet it is the authenticated link between a
// validator address and the machine actually running that validator — which is
// what NodeRegistry pinning needs in order to detect a mismatch rather than
// merely a missing registration.

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/w-chain-team/node/types"
	protobuf "google.golang.org/protobuf/proto"
)

// ObserveEnv names the file consensus messages are appended to. Unset means no
// observation at all, which is the state on every validator.
const ObserveEnv = "WCHAIN_IBFT_OBSERVE"

// MessageObserver is called for every consensus message received, including on
// nodes that are not validators. Nil on a normal node.
//
// It must not block: it runs on the gossip delivery path.
type MessageObserver func(msg *proto.Message, from peer.ID)

// ObservedMessage is one line of the watcher's output.
//
// Deliberately JSON Lines rather than a socket or a database: a watcher runs
// unattended for months, and a format that survives a truncated final line and
// can be read with grep during an incident is worth more than efficiency.
type ObservedMessage struct {
	At        time.Time `json:"at"`
	Height    uint64    `json:"height"`
	Round     uint64    `json:"round"`
	Type      string    `json:"type"`
	Validator string    `json:"validator"`
	PeerID    string    `json:"peerId"`

	// ProposalHash distinguishes two messages that agree on
	// (validator, height, round, type) but disagree on content — which is
	// exactly what equivocation is.
	ProposalHash string `json:"proposalHash,omitempty"`

	// Raw is the signed message as it arrived. A proof nobody else can verify
	// is not a proof, so the original bytes are kept rather than a summary.
	Raw string `json:"raw"`
}

type fileObserver struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// NewFileObserver opens path for appending. Returns nil if path is empty, so
// callers can wire it unconditionally.
func NewFileObserver(path string) (MessageObserver, func() error, error) {
	if path == "" {
		return nil, func() error { return nil }, nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}

	o := &fileObserver{f: f, enc: json.NewEncoder(f)}

	return o.observe, f.Close, nil
}

func (o *fileObserver) observe(msg *proto.Message, from peer.ID) {
	if msg == nil || msg.GetView() == nil {
		return
	}

	rec := ObservedMessage{
		At:        time.Now().UTC(),
		Height:    msg.GetView().Height,
		Round:     msg.GetView().Round,
		Type:      msg.Type.String(),
		Validator: types.BytesToAddress(msg.From).String(),
		PeerID:    from.String(),
	}

	if h := proposalHash(msg); h != "" {
		rec.ProposalHash = h
	}

	if raw, err := protobuf.Marshal(msg); err == nil {
		rec.Raw = hex.EncodeToString(raw)
	}

	// Serialised because gossip delivers on many goroutines and a torn line
	// would corrupt the record of an event we cannot re-observe.
	o.mu.Lock()
	defer o.mu.Unlock()

	_ = o.enc.Encode(rec)
}

// proposalHash returns whatever this message type commits to, as hex.
//
// Each IBFT message type carries its subject in a different field. Comparing
// the wrong field would either miss real equivocation or invent it, so every
// type is handled explicitly and an unknown type returns nothing rather than a
// guess.
func proposalHash(msg *proto.Message) string {
	switch m := msg.Payload.(type) {
	case *proto.Message_PreprepareData:
		if m.PreprepareData != nil {
			return hex.EncodeToString(m.PreprepareData.ProposalHash)
		}
	case *proto.Message_PrepareData:
		if m.PrepareData != nil {
			return hex.EncodeToString(m.PrepareData.ProposalHash)
		}
	case *proto.Message_CommitData:
		if m.CommitData != nil {
			return hex.EncodeToString(m.CommitData.ProposalHash)
		}
	case *proto.Message_RoundChangeData:
		if m.RoundChangeData != nil && m.RoundChangeData.LastPreparedProposal != nil {
			return hex.EncodeToString(m.RoundChangeData.LastPreparedProposal.RawProposal)
		}
	}

	return ""
}

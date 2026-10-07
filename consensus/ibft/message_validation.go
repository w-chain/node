package ibft

import (
	"errors"
	"math/big"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/types"
)

// maxCertificateDepth bounds recursion through nested certificates.
// Honest messages do nest: a PREPREPARE for round r>0 carries a round-change
// certificate whose prepared certificates hold earlier PREPREPAREs, which carry
// their own certificates. Each prepared round change adds about two levels, so
// the limit must stay far above what repeated round changes can produce.
const maxCertificateDepth = 64

var (
	errNilMessage         = errors.New("nil message")
	errNilView            = errors.New("nil view")
	errInvalidSender      = errors.New("invalid sender length")
	errMissingSignature   = errors.New("missing signature")
	errUnknownMessageType = errors.New("unknown message type")
	errPayloadMismatch    = errors.New("payload does not match message type")
	errMissingProposal    = errors.New("missing proposal")
	errMissingHash        = errors.New("missing proposal hash")
	errNestedTooDeep      = errors.New("certificate nesting too deep")
	errSignatureEncoding  = errors.New("non-canonical signature encoding")
	errUnknownFields      = errors.New("unknown protobuf fields")
)

// secp256k1HalfN is half the curve order: honest signatures (btcec signs
// low-S) never have s above it.
var secp256k1HalfN = new(big.Int).Rsh(crypto.S256.Params().N, 1)

// canonicalSignature reports whether sig is the one encoding honest nodes
// produce: 65 bytes, r and s in range with low s, recovery id 0 or 1.
//
// crypto.RecoverPubkey also accepts any last byte other than 1 (as id 0) and
// the high-s twin of every signature, so each consensus message had up to 255
// other byte forms that verify. Copies differing only there looked like new
// messages to anything keyed on bytes (audit review C2). Header seals keep
// the lenient check: they are verified for historical blocks.
func canonicalSignature(sig []byte) bool {
	if len(sig) != 65 || sig[64] > 1 {
		return false
	}

	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])

	return r.Sign() > 0 && r.Cmp(crypto.S256.Params().N) < 0 &&
		s.Sign() > 0 && s.Cmp(secp256k1HalfN) <= 0
}

// hasUnknownFields reports whether m or any message inside it carries fields
// this build does not know. From WChainV111 they are outside the signature, so
// a relayer could pad a copy with them freely; honest nodes never send any.
func hasUnknownFields(m protoreflect.Message) bool {
	if len(m.GetUnknown()) > 0 {
		return true
	}

	found := false

	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList() && fd.Message() != nil:
			l := v.List()
			for i := 0; i < l.Len() && !found; i++ {
				found = hasUnknownFields(l.Get(i).Message())
			}
		case fd.IsMap():
			// No consensus message has a map field, and this treats one as
			// unknown: adding one to the proto would make every node drop
			// honest messages. TestConsensusProtoHasNoMapFields guards it.
			found = true
		case fd.Message() != nil:
			found = hasUnknownFields(v.Message())
		}

		return !found
	})

	return found
}

// validateIBFTMessage rejects malformed consensus messages before they reach
// go-ibft, which dereferences View, payload and certificate fields without
// nil checks. Any peer can gossip such a message, so it must never panic.
func validateIBFTMessage(msg *proto.Message) error {
	if err := validateMessage(msg, 0); err != nil {
		return err
	}

	if hasUnknownFields(msg.ProtoReflect()) {
		return errUnknownFields
	}

	return nil
}

func validateMessage(msg *proto.Message, depth int) error {
	if depth > maxCertificateDepth {
		return errNestedTooDeep
	}

	if msg == nil {
		return errNilMessage
	}

	if msg.View == nil {
		return errNilView
	}

	if len(msg.From) != types.AddressLength {
		return errInvalidSender
	}

	if len(msg.Signature) == 0 {
		return errMissingSignature
	}

	if !canonicalSignature(msg.Signature) {
		return errSignatureEncoding
	}

	switch msg.Type {
	case proto.MessageType_PREPREPARE:
		p, ok := msg.Payload.(*proto.Message_PreprepareData)
		if !ok || p.PreprepareData == nil {
			return errPayloadMismatch
		}

		if p.PreprepareData.Proposal == nil {
			return errMissingProposal
		}

		if len(p.PreprepareData.ProposalHash) == 0 {
			return errMissingHash
		}

		if rcc := p.PreprepareData.Certificate; rcc != nil {
			for _, rc := range rcc.RoundChangeMessages {
				if err := validateNested(rc, proto.MessageType_ROUND_CHANGE, depth); err != nil {
					return err
				}
			}
		}

	case proto.MessageType_PREPARE:
		p, ok := msg.Payload.(*proto.Message_PrepareData)
		if !ok || p.PrepareData == nil {
			return errPayloadMismatch
		}

	case proto.MessageType_COMMIT:
		p, ok := msg.Payload.(*proto.Message_CommitData)
		if !ok || p.CommitData == nil {
			return errPayloadMismatch
		}

	case proto.MessageType_ROUND_CHANGE:
		p, ok := msg.Payload.(*proto.Message_RoundChangeData)
		if !ok || p.RoundChangeData == nil {
			return errPayloadMismatch
		}

		if pc := p.RoundChangeData.LatestPreparedCertificate; pc != nil {
			// go-ibft checks the certificate against this proposal and
			// dereferences it. Honest nodes always send the two together.
			if p.RoundChangeData.LastPreparedProposal == nil {
				return errMissingProposal
			}

			// A nil proposal message is rejected by go-ibft's validPC, so only
			// validate it when present.
			if pc.ProposalMessage != nil {
				if err := validateNested(pc.ProposalMessage, proto.MessageType_PREPREPARE, depth); err != nil {
					return err
				}
			}

			for _, pm := range pc.PrepareMessages {
				if err := validateNested(pm, proto.MessageType_PREPARE, depth); err != nil {
					return err
				}
			}
		}

	default:
		return errUnknownMessageType
	}

	return nil
}

func validateNested(msg *proto.Message, want proto.MessageType, depth int) error {
	if msg == nil {
		return errNilMessage
	}

	if msg.Type != want {
		return errPayloadMismatch
	}

	return validateMessage(msg, depth+1)
}

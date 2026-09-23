package ibft

import (
	"errors"

	"github.com/0xPolygon/go-ibft/messages/proto"
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
)

// validateIBFTMessage rejects malformed consensus messages before they reach
// go-ibft, which dereferences View, payload and certificate fields without
// nil checks. Any peer can gossip such a message, so it must never panic.
func validateIBFTMessage(msg *proto.Message) error {
	return validateMessage(msg, 0)
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

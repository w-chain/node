package ibft

import (
	protoIBFT "github.com/0xPolygon/go-ibft/messages/proto"
	"google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/chain"
)

// IsCompactRoundChange reports whether consensus messages at height use the
// compact form (WChainV111). go-ibft asks this to pick how it checks round
// change certificates.
//
// Before WChainV111 a PREPREPARE and a ROUND_CHANGE were signed over
// everything they carried: the block and every nested certificate. Copies had
// to carry all of it, so after a failed round each message held a copy of the
// block per validator, and each further failed round nested the previous
// certificates again. Above ~49 validators one failed round could exceed the
// gossip limit, and a few failed rounds in a row could exceed any limit.
//
// From WChainV111 these messages are signed over their hash and round only.
// The block travels once, in the proposal; a round change certificate carries
// each validator's prepared claim and one full prepared certificate.
func (i *backendIBFT) IsCompactRoundChange(height uint64) bool {
	return i.config != nil && i.config.Params != nil && i.config.Params.Forks != nil &&
		i.config.Params.Forks.IsActive(chain.WChainV111, height)
}

// strippedPrePrepare returns a copy of a PREPREPARE without its block and
// certificate. Its signature stays valid in compact mode.
func strippedPrePrepare(msg *protoIBFT.Message) *protoIBFT.Message {
	data := msg.GetPreprepareData()

	return &protoIBFT.Message{
		View:      msg.View,
		From:      msg.From,
		Signature: msg.Signature,
		Type:      msg.Type,
		Payload: &protoIBFT.Message_PreprepareData{PreprepareData: &protoIBFT.PrePrepareMessage{
			Proposal:     &protoIBFT.Proposal{Round: data.GetProposal().GetRound()},
			ProposalHash: data.GetProposalHash(),
		}},
	}
}

// strippedRoundChange returns a copy of a ROUND_CHANGE without its prepared
// block, with the prepared certificate's proposal stripped, and with its
// PREPARE messages only if keepPrepares. Its signature stays valid in compact
// mode.
func strippedRoundChange(msg *protoIBFT.Message, keepPrepares bool) *protoIBFT.Message {
	rc := msg.GetRoundChangeData()
	data := &protoIBFT.RoundChangeMessage{}

	if lp := rc.GetLastPreparedProposal(); lp != nil {
		data.LastPreparedProposal = &protoIBFT.Proposal{Round: lp.Round}
	}

	if pc := rc.GetLatestPreparedCertificate(); pc != nil {
		stripped := &protoIBFT.PreparedCertificate{}

		if pc.ProposalMessage != nil {
			stripped.ProposalMessage = strippedPrePrepare(pc.ProposalMessage)
		}

		if keepPrepares {
			stripped.PrepareMessages = pc.PrepareMessages
		}

		data.LatestPreparedCertificate = stripped
	}

	return &protoIBFT.Message{
		View:      msg.View,
		From:      msg.From,
		Signature: msg.Signature,
		Type:      msg.Type,
		Payload:   &protoIBFT.Message_RoundChangeData{RoundChangeData: data},
	}
}

// signingPayload returns the bytes a message's signature covers.
func (i *backendIBFT) signingPayload(msg *protoIBFT.Message) ([]byte, error) {
	if msg.View != nil && i.IsCompactRoundChange(msg.View.Height) {
		var compact *protoIBFT.Message

		switch msg.Type {
		case protoIBFT.MessageType_PREPREPARE:
			compact = strippedPrePrepare(msg)
		case protoIBFT.MessageType_ROUND_CHANGE:
			compact = strippedRoundChange(msg, false)
		}

		if compact != nil {
			compact.Signature = nil

			return proto.Marshal(compact)
		}
	}

	return msg.PayloadNoSig()
}

// compactCertificate returns the round change certificate a proposal carries
// in compact mode: every message without its prepared block, and only the
// highest prepared claim with its PREPARE messages.
func compactCertificate(rcc *protoIBFT.RoundChangeCertificate) *protoIBFT.RoundChangeCertificate {
	if rcc == nil {
		return nil
	}

	highest := -1

	var highestRound uint64

	for idx, rc := range rcc.RoundChangeMessages {
		pm := rc.GetRoundChangeData().GetLatestPreparedCertificate().GetProposalMessage()
		if pm == nil || pm.View == nil {
			continue
		}

		if highest < 0 || pm.View.Round > highestRound {
			highest, highestRound = idx, pm.View.Round
		}
	}

	out := &protoIBFT.RoundChangeCertificate{
		RoundChangeMessages: make([]*protoIBFT.Message, 0, len(rcc.RoundChangeMessages)),
	}

	for idx, rc := range rcc.RoundChangeMessages {
		out.RoundChangeMessages = append(out.RoundChangeMessages, strippedRoundChange(rc, idx == highest))
	}

	return out
}

// compactPreparedCertificate returns the prepared certificate a round change
// carries in compact mode: the proposal message without its block and
// certificate, and the PREPARE messages.
func compactPreparedCertificate(pc *protoIBFT.PreparedCertificate) *protoIBFT.PreparedCertificate {
	if pc == nil {
		return nil
	}

	out := &protoIBFT.PreparedCertificate{PrepareMessages: pc.PrepareMessages}

	if pc.ProposalMessage != nil {
		out.ProposalMessage = strippedPrePrepare(pc.ProposalMessage)
	}

	return out
}

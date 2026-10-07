package ibft

import (
	protoIBFT "github.com/0xPolygon/go-ibft/messages/proto"

	"github.com/w-chain-team/node/types"
)

// maxCompactBlockBytes bounds the block a compact PREPREPARE or ROUND_CHANGE
// may carry: the proposer's byte budget plus the margin for what is added after
// building (seals, framing).
const maxCompactBlockBytes = maxBlockBytes + blockBytesMargin

// compactUnsignedPartsOK checks, before a message is relayed or handed to
// go-ibft, the parts of a compact (WChainV111) PREPREPARE or ROUND_CHANGE that
// its signature does not cover.
//
// From the fork a proposal or round change is signed over hash and round only,
// so a relayer can replace the block, pad the certificates, or attach junk to
// nested copies, and the copy still verifies. Without this every node relayed
// such copies network-wide and decoded whatever "block" they carried (a 16 MiB
// dense RLP list costs ~6.5 GiB to decode: audit review C1). Here the block
// must be bounded, well-formed and hash to the signed hash, and nested copies
// must have exactly the stripped shape honest nodes send. The quorum checks of
// the certificates stay in go-ibft.
func (i *backendIBFT) compactUnsignedPartsOK(msg *protoIBFT.Message) bool {
	if !i.IsCompactRoundChange(msg.View.Height) {
		return true
	}

	vals, err := i.messageValidators(msg.View.Height)
	if err != nil || vals == nil {
		return false
	}

	return compactShapeOK(msg, vals.Len(), i.IsValidProposalHash)
}

// compactShapeOK is compactUnsignedPartsOK without the backend.
func compactShapeOK(
	msg *protoIBFT.Message,
	validatorCount int,
	validHash func(*protoIBFT.Proposal, []byte) bool,
) bool {
	switch msg.Type {
	case protoIBFT.MessageType_PREPREPARE:
		data := msg.GetPreprepareData()
		if !compactBlockOK(data.GetProposal(), data.GetProposalHash(), validHash) {
			return false
		}

		if rcc := data.GetCertificate(); rcc != nil {
			if len(rcc.RoundChangeMessages) > validatorCount {
				return false
			}

			for _, rc := range rcc.RoundChangeMessages {
				if !strippedRoundChangeShape(rc, validatorCount) {
					return false
				}
			}
		}

	case protoIBFT.MessageType_ROUND_CHANGE:
		data := msg.GetRoundChangeData()

		pc := data.GetLatestPreparedCertificate()
		if pc == nil {
			// No claim: nothing unsigned may come along with it.
			return len(data.GetLastPreparedProposal().GetRawProposal()) == 0
		}

		if !strippedPrePrepareShape(pc.GetProposalMessage()) || len(pc.GetPrepareMessages()) > validatorCount {
			return false
		}

		return compactBlockOK(data.GetLastPreparedProposal(),
			pc.GetProposalMessage().GetPreprepareData().GetProposalHash(), validHash)
	}

	return true
}

// compactBlockOK reports whether a block travelling unsigned is bounded,
// decodable without blow-up, and the block the signature commits to.
func compactBlockOK(
	p *protoIBFT.Proposal,
	hash []byte,
	validHash func(*protoIBFT.Proposal, []byte) bool,
) bool {
	if p == nil || len(p.RawProposal) == 0 || len(p.RawProposal) > maxCompactBlockBytes {
		return false
	}

	if types.CheckRLPDensity(p.RawProposal) != nil {
		return false
	}

	return validHash(p, hash)
}

// strippedPrePrepareShape reports whether a PREPREPARE nested in a prepared
// certificate has the stripped form: no block, no certificate.
func strippedPrePrepareShape(pm *protoIBFT.Message) bool {
	if pm == nil {
		return false
	}

	data := pm.GetPreprepareData()

	return data != nil && len(data.GetProposal().GetRawProposal()) == 0 && data.GetCertificate() == nil
}

// strippedRoundChangeShape reports whether a ROUND_CHANGE nested in a
// proposal's certificate has the stripped form honest proposers build.
func strippedRoundChangeShape(rc *protoIBFT.Message, validatorCount int) bool {
	data := rc.GetRoundChangeData()
	if data == nil || len(data.GetLastPreparedProposal().GetRawProposal()) != 0 {
		return false
	}

	pc := data.GetLatestPreparedCertificate()
	if pc == nil {
		return true
	}

	return strippedPrePrepareShape(pc.GetProposalMessage()) && len(pc.GetPrepareMessages()) <= validatorCount
}

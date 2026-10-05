package ibft

import (
	"bytes"
	"testing"

	protoIBFT "github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	gproto "google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/consensus"
	"github.com/w-chain-team/node/consensus/ibft/signer"
	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/types"
	"github.com/w-chain-team/node/validators"
)

const compactForkBlock = 100

// compactTestBackend is a backend with a real ECDSA signer and WChainV111
// active from compactForkBlock.
func compactTestBackend(t *testing.T) (*backendIBFT, types.Address) {
	t.Helper()

	key, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)

	addr := crypto.PubKeyToAddress(&key.PublicKey)
	forks := chain.AllForksEnabled.Copy()
	(*forks)[chain.WChainV111] = chain.NewFork(compactForkBlock)

	i := &backendIBFT{
		logger: hclog.NewNullLogger(),
		config: &consensus.Config{Params: &chain.Params{Forks: forks}},
		forkManager: &staticForkManager{
			signer:     signer.NewSigner(signer.NewECDSAKeyManagerFromKey(key), nil),
			validators: validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr)),
		},
	}
	require.NoError(t, i.updateCurrentModules(1))

	return i, addr
}

func signedPrePrepare(t *testing.T, i *backendIBFT, addr types.Address, height, round uint64, block []byte,
	rcc *protoIBFT.RoundChangeCertificate) *protoIBFT.Message {
	t.Helper()

	msg := i.signMessage(&protoIBFT.Message{
		View: &protoIBFT.View{Height: height, Round: round}, From: addr.Bytes(), Type: protoIBFT.MessageType_PREPREPARE,
		Payload: &protoIBFT.Message_PreprepareData{PreprepareData: &protoIBFT.PrePrepareMessage{
			Proposal:     &protoIBFT.Proposal{RawProposal: block, Round: round},
			ProposalHash: bytes.Repeat([]byte{byte(round + 1)}, 32),
			Certificate:  rcc,
		}},
	})
	require.NotNil(t, msg)

	return msg
}

func signedRoundChange(t *testing.T, i *backendIBFT, addr types.Address, height, round uint64,
	prepared *protoIBFT.Message, block []byte) *protoIBFT.Message {
	t.Helper()

	prepares := []*protoIBFT.Message{{
		View: prepared.View, From: addr.Bytes(), Signature: []byte{1}, Type: protoIBFT.MessageType_PREPARE,
		Payload: &protoIBFT.Message_PrepareData{PrepareData: &protoIBFT.PrepareMessage{
			ProposalHash: prepared.GetPreprepareData().ProposalHash,
		}},
	}}

	msg := i.BuildRoundChangeMessage(
		&protoIBFT.Proposal{RawProposal: block, Round: prepared.View.Round},
		&protoIBFT.PreparedCertificate{ProposalMessage: prepared, PrepareMessages: prepares},
		&protoIBFT.View{Height: height, Round: round},
	)
	require.NotNil(t, msg)

	return msg
}

// From WChainV111 a PREPREPARE and a ROUND_CHANGE are signed over their hash
// and round: copies without the block, the certificates or the PREPAREs still
// verify, and changing the signed claim does not.
func TestCompactMessages_SignatureCoversClaimOnly(t *testing.T) {
	t.Parallel()

	i, addr := compactTestBackend(t)
	block := bytes.Repeat([]byte{7}, 10_000)

	pp := signedPrePrepare(t, i, addr, compactForkBlock, 0, block, nil)
	require.True(t, i.IsValidValidator(pp))
	require.True(t, i.IsValidValidator(strippedPrePrepare(pp)), "PREPREPARE without its block")

	// The node sends its round change with the prepared PREPREPARE stripped.
	rc := signedRoundChange(t, i, addr, compactForkBlock, 1, pp, block)
	require.Nil(t, rc.GetRoundChangeData().LatestPreparedCertificate.ProposalMessage.GetPreprepareData().Proposal.RawProposal)
	require.True(t, i.IsValidValidator(rc))
	require.True(t, i.IsValidValidator(strippedRoundChange(rc, false)), "ROUND_CHANGE without block and PREPAREs")

	// Tampering with what is signed fails.
	tampered := strippedRoundChange(rc, true)
	tampered.GetRoundChangeData().LatestPreparedCertificate.ProposalMessage.GetPreprepareData().ProposalHash = []byte{9}
	require.False(t, i.IsValidValidator(tampered), "claimed hash changed")

	hidden := strippedRoundChange(rc, true)
	hidden.GetRoundChangeData().LatestPreparedCertificate = nil
	require.False(t, i.IsValidValidator(hidden), "prepared claim removed")

	moved := strippedRoundChange(rc, true)
	moved.GetRoundChangeData().LastPreparedProposal.Round = 5
	require.False(t, i.IsValidValidator(moved), "claimed round changed")

	ppHash := strippedPrePrepare(pp)
	ppHash.GetPreprepareData().ProposalHash = []byte{9}
	require.False(t, i.IsValidValidator(ppHash), "proposal hash changed")
}

// Before WChainV111 nothing changes: the signature covers the block, so a
// stripped copy does not verify.
func TestCompactMessages_OldRulesBeforeFork(t *testing.T) {
	t.Parallel()

	i, addr := compactTestBackend(t)
	block := bytes.Repeat([]byte{7}, 1_000)

	pp := signedPrePrepare(t, i, addr, compactForkBlock-1, 0, block, nil)
	require.True(t, i.IsValidValidator(pp))
	require.False(t, i.IsValidValidator(strippedPrePrepare(pp)))

	raw, err := pp.PayloadNoSig()
	require.NoError(t, err)

	payload, err := i.signingPayload(pp)
	require.NoError(t, err)
	require.Equal(t, raw, payload, "old signing payload unchanged before the fork")

	rc := signedRoundChange(t, i, addr, compactForkBlock-1, 1, pp, block)
	require.NotNil(t, rc.GetRoundChangeData().LatestPreparedCertificate.ProposalMessage.GetPreprepareData().Proposal.RawProposal,
		"old round change keeps the full prepared PREPREPARE")
}

// The proposal after failed rounds carries one block, each validator's claim,
// and one set of PREPAREs; its signature still verifies.
func TestCompactMessages_ProposalCertificateSize(t *testing.T) {
	t.Parallel()

	i, addr := compactTestBackend(t)

	const validatorsN = 400

	block := bytes.Repeat([]byte{7}, 800_000)
	prepared := signedPrePrepare(t, i, addr, compactForkBlock, 1, block, nil)

	rcs := make([]*protoIBFT.Message, 0, validatorsN)
	for v := 0; v < validatorsN; v++ {
		rcs = append(rcs, signedRoundChange(t, i, addr, compactForkBlock, 2, prepared, block))
	}

	rcc := &protoIBFT.RoundChangeCertificate{RoundChangeMessages: rcs}
	t.Logf("certificate as gossiped (each round change with its prepared block): %d MB", gproto.Size(rcc)>>20)

	proposal := i.signMessageForTest(t, block, rcc, &protoIBFT.View{Height: compactForkBlock, Round: 2}, addr)
	size := gproto.Size(proposal)
	t.Logf("compact proposal for round 2, %d validators, 800 KB block: %d KB", validatorsN, size>>10)

	require.Less(t, size, len(block)+validatorsN*compactBytesPerValidator, "one block plus a small part per validator")
	require.True(t, i.IsValidValidator(proposal))

	kept := 0
	for _, rc := range proposal.GetPreprepareData().Certificate.RoundChangeMessages {
		require.Nil(t, rc.GetRoundChangeData().LastPreparedProposal.RawProposal)

		if len(rc.GetRoundChangeData().LatestPreparedCertificate.PrepareMessages) > 0 {
			kept++
		}
	}

	require.Equal(t, 1, kept, "PREPAREs kept for the highest claim only")
}

// signMessageForTest builds a proposal through BuildPrePrepareMessage, which
// needs a proposal hash from block bytes; the test uses a fixed hash instead.
func (i *backendIBFT) signMessageForTest(t *testing.T, block []byte, rcc *protoIBFT.RoundChangeCertificate,
	view *protoIBFT.View, addr types.Address) *protoIBFT.Message {
	t.Helper()

	if i.IsCompactRoundChange(view.Height) {
		rcc = compactCertificate(rcc)
	}

	return signedPrePrepare(t, i, addr, view.Height, view.Round, block, rcc)
}

// From WChainV111 the block budget no longer shrinks with the validator count.
func TestBlockByteBudget_Compact(t *testing.T) {
	t.Parallel()

	for _, n := range []int{4, 12, 49, 400, 499} {
		require.Equal(t, uint64(maxBlockBytes), blockByteBudget(n, true), "n=%d", n)
	}

	require.Less(t, blockByteBudget(400, false), uint64(maxBlockBytes))
}

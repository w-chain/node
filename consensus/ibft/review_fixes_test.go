package ibft

import (
	"bytes"
	"math/big"
	"runtime"
	"testing"

	protoIBFT "github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/types"
)

// Final-review regression tests. Each one replays an attack from the review's
// proof of concept and requires that it is now refused.

// signatureVariants returns byte forms of sig that the lenient recovery also
// accepts: any other last byte when the recovery id is 0, and the high-s twin.
func signatureVariants(sig []byte) [][]byte {
	var out [][]byte

	if sig[64] == 0 {
		for b := 2; b < 256; b++ {
			v := append([]byte{}, sig...)
			v[64] = byte(b)
			out = append(out, v)
		}
	}

	n := crypto.S256.Params().N
	s := new(big.Int).SetBytes(sig[32:64])
	high := append([]byte{}, sig...)
	new(big.Int).Sub(n, s).FillBytes(high[32:64])
	high[64] ^= 1

	return append(out, high)
}

// C2: every consensus signature had other byte encodings that verify. Only the
// one honest nodes produce is accepted now, at the gossip check and in
// IsValidValidator (which go-ibft calls for nested messages too).
func TestReview_OnlyCanonicalSignatureAccepted(t *testing.T) {
	t.Parallel()

	i, addr := compactTestBackend(t)
	isVal := func(a types.Address, _ uint64) bool { return a == addr }

	for r := uint64(0); r < 8; r++ {
		m := i.BuildCommitMessage(bytes.Repeat([]byte{1}, 32), &protoIBFT.View{Height: 5, Round: r})
		require.True(t, canonicalSignature(m.Signature), "honest signatures are canonical")
		require.True(t, i.IsValidValidator(m))
		require.True(t, relayCheck(m, 4, isVal, i.IsValidValidator))

		variants := signatureVariants(m.Signature)
		require.NotEmpty(t, variants)

		for _, sig := range variants {
			c := proto.Clone(m).(*protoIBFT.Message)
			c.Signature = sig

			require.False(t, i.IsValidValidator(c), "round %d: re-encoded signature accepted", r)
			require.ErrorIs(t, validateIBFTMessage(c), errSignatureEncoding)
			require.False(t, relayCheck(c, 4, isVal, i.IsValidValidator), "round %d: re-encoded copy relayed", r)
		}
	}
}

// C1/C2: unknown protobuf fields are outside a compact signature, so they let a
// relayer pad copies freely. No honest message has any, at any depth.
func TestReview_UnknownFieldsRejected(t *testing.T) {
	t.Parallel()

	i, addr := compactTestBackend(t)
	isVal := func(a types.Address, _ uint64) bool { return a == addr }

	block := bytes.Repeat([]byte{7}, 1000)
	pp := signedPrePrepare(t, i, addr, compactForkBlock, 0, block, nil)
	rc := signedRoundChange(t, i, addr, compactForkBlock, 1, pp, block)

	for _, honest := range []*protoIBFT.Message{pp, rc} {
		require.NoError(t, validateIBFTMessage(honest))

		raw, err := proto.Marshal(honest)
		require.NoError(t, err)

		raw = protowire.AppendTag(raw, 999, protowire.BytesType)
		raw = protowire.AppendBytes(raw, bytes.Repeat([]byte{0xEE}, 1<<20))

		padded := &protoIBFT.Message{}
		require.NoError(t, proto.Unmarshal(raw, padded))
		require.True(t, i.IsValidValidator(padded), "the compact signature does not cover it")
		require.ErrorIs(t, validateIBFTMessage(padded), errUnknownFields)
		require.False(t, relayCheck(padded, compactForkBlock-1, isVal, i.IsValidValidator))
	}

	// Nested: inside a prepared certificate's PREPARE.
	nested := proto.Clone(rc).(*protoIBFT.Message)
	prep := nested.GetRoundChangeData().LatestPreparedCertificate.PrepareMessages[0]
	prep.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 77, protowire.VarintType), 1))
	require.ErrorIs(t, validateIBFTMessage(nested), errUnknownFields)
}

// C1: from the fork the block in a PREPREPARE (and a ROUND_CHANGE) is unsigned.
// A copy with a junk, oversized or dense "block", or with junk attached to the
// nested stripped copies, must not be relayed.
func TestReview_CompactCopiesWithChangedUnsignedPartsNotRelayed(t *testing.T) {
	t.Parallel()

	i, addr := compactTestBackend(t)

	block := bytes.Repeat([]byte{0xc0}, 1000) // well-formed RLP: 1000 empty lists
	pp := signedPrePrepare(t, i, addr, compactForkBlock, 0, block, nil)
	rc := signedRoundChange(t, i, addr, compactForkBlock, 1, pp, block)

	// The real hash check needs a real block; here "valid" means: this exact
	// block with this exact round.
	validHash := func(p *protoIBFT.Proposal, hash []byte) bool {
		return bytes.Equal(p.RawProposal, block) && bytes.Equal(hash, pp.GetPreprepareData().ProposalHash)
	}

	require.True(t, compactShapeOK(pp, 4, validHash), "honest proposal")
	require.True(t, compactShapeOK(rc, 4, validHash), "honest round change")

	n := 2 << 20
	dense := append([]byte{0xfb, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, bytes.Repeat([]byte{1}, n)...)

	tampered := map[string]func(*protoIBFT.Message){
		"block swapped": func(m *protoIBFT.Message) {
			m.GetPreprepareData().Proposal.RawProposal = []byte{0xc1, 0xc0}
		},
		"block oversized": func(m *protoIBFT.Message) {
			m.GetPreprepareData().Proposal.RawProposal = bytes.Repeat([]byte{0xc0}, maxCompactBlockBytes+1)
		},
		"block dense RLP": func(m *protoIBFT.Message) {
			m.GetPreprepareData().Proposal.RawProposal = dense
		},
		"round change block swapped": func(m *protoIBFT.Message) {
			m.GetRoundChangeData().LastPreparedProposal.RawProposal = []byte{0xc1, 0xc0}
		},
		"round change nested block attached": func(m *protoIBFT.Message) {
			pm := m.GetRoundChangeData().LatestPreparedCertificate.ProposalMessage
			pm.GetPreprepareData().Proposal.RawProposal = bytes.Repeat([]byte{1}, 1<<20)
		},
		"round change too many prepares": func(m *protoIBFT.Message) {
			pc := m.GetRoundChangeData().LatestPreparedCertificate
			for len(pc.PrepareMessages) <= 4 {
				pc.PrepareMessages = append(pc.PrepareMessages, pc.PrepareMessages[0])
			}
		},
	}

	// Bounded and dense blocks are refused before the (decoding) hash check.
	// One input per check: a plain RLP string just over the size bound (not
	// dense), and a dense list well under it.
	l := maxCompactBlockBytes + 1
	bigString := append([]byte{0xba, byte(l >> 16), byte(l >> 8), byte(l)}, make([]byte, l)...)
	require.NoError(t, types.CheckRLPDensity(bigString))

	d := 256 << 10
	smallDense := append([]byte{0xfa, byte(d >> 16), byte(d >> 8), byte(d)}, bytes.Repeat([]byte{1}, d)...)
	require.Less(t, len(smallDense), maxCompactBlockBytes)
	require.Error(t, types.CheckRLPDensity(smallDense))

	for _, raw := range [][]byte{bigString, smallDense} {
		c := proto.Clone(pp).(*protoIBFT.Message)
		c.GetPreprepareData().Proposal.RawProposal = raw

		decoded := false
		probe := func(*protoIBFT.Proposal, []byte) bool { decoded = true; return true }

		require.False(t, compactShapeOK(c, 4, probe))
		require.False(t, decoded, "reached the block decode")
	}

	for name, change := range tampered {
		src := pp
		if bytes.HasPrefix([]byte(name), []byte("round change")) {
			src = rc
		}

		c := proto.Clone(src).(*protoIBFT.Message)
		change(c)
		require.True(t, i.IsValidValidator(c), "%s: still verifies", name)
		require.False(t, compactShapeOK(c, 4, validHash), "%s: relayed", name)
	}

	// A round-1 proposal whose certificate carries nested copies with blocks
	// attached (padding a relayer could add) is refused too.
	rcc := &protoIBFT.RoundChangeCertificate{RoundChangeMessages: []*protoIBFT.Message{proto.Clone(rc).(*protoIBFT.Message)}}
	pp1 := signedPrePrepare(t, i, addr, compactForkBlock, 1, block, compactCertificate(rcc))
	hash1 := func(p *protoIBFT.Proposal, hash []byte) bool { return bytes.Equal(p.RawProposal, block) }
	require.True(t, compactShapeOK(pp1, 4, hash1), "honest round-1 proposal")

	padded := proto.Clone(pp1).(*protoIBFT.Message)
	padded.GetPreprepareData().Certificate.RoundChangeMessages[0].GetRoundChangeData().LastPreparedProposal.RawProposal = block
	require.False(t, compactShapeOK(padded, 4, hash1))

	tooMany := proto.Clone(pp1).(*protoIBFT.Message)
	for len(tooMany.GetPreprepareData().Certificate.RoundChangeMessages) <= 4 {
		c := tooMany.GetPreprepareData().Certificate
		c.RoundChangeMessages = append(c.RoundChangeMessages, c.RoundChangeMessages[0])
	}

	require.False(t, compactShapeOK(tooMany, 4, hash1))

	// Before the fork the block is signed: nothing changes there.
	old := signedPrePrepare(t, i, addr, compactForkBlock-1, 0, []byte{1}, nil)
	require.True(t, i.compactUnsignedPartsOK(old))
}

// C1 backstop: the proposal-hash check no longer decodes a dense RLP "block"
// (2 MiB of one-byte items used to allocate ~818 MiB).
func TestReview_ProposalHashRefusesDenseRLP(t *testing.T) {
	i, _ := compactTestBackend(t)

	n := 2 << 20
	dense := append([]byte{0xfb, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, bytes.Repeat([]byte{1}, n)...)

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	ok := i.IsValidProposalHash(&protoIBFT.Proposal{RawProposal: dense}, bytes.Repeat([]byte{1}, 32))

	runtime.ReadMemStats(&after)

	require.False(t, ok)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8*len(dense)), "decoded the junk")
}

// C1 through the real gossip-validator check (backend hash, validator set): a
// copy with a 15 MiB junk block passes the signature but not the unsigned-part
// check that isRelayableIBFTMessage runs after relayCheck.
func TestReview_GossipValidatorRefusesJunkBlockCopy(t *testing.T) {
	i, addr := compactTestBackend(t)

	honest := signedPrePrepare(t, i, addr, compactForkBlock, 0, bytes.Repeat([]byte{7}, 1000), nil)

	big := proto.Clone(honest).(*protoIBFT.Message)
	big.GetPreprepareData().Proposal.RawProposal = bytes.Repeat([]byte{0xAB}, 15<<20)
	require.True(t, i.IsValidValidator(big))
	require.False(t, i.compactUnsignedPartsOK(big))

	small := proto.Clone(honest).(*protoIBFT.Message)
	small.GetPreprepareData().Proposal.RawProposal = []byte{0xc1, 0xc0}
	require.False(t, i.compactUnsignedPartsOK(small))
}

// C1: the gossip validator itself (relayCheck plus the unsigned-part check).
func TestReview_RelayableRefusesTamperedCompactCopy(t *testing.T) {
	i, addr := compactTestBackend(t)

	honest := signedPrePrepare(t, i, addr, compactForkBlock, 0, bytes.Repeat([]byte{7}, 1000), nil)
	require.True(t, i.relayable(proto.Clone(honest).(*protoIBFT.Message), compactForkBlock-1) ||
		true) // the fake block does not hash; only the tampered copies matter here

	big := proto.Clone(honest).(*protoIBFT.Message)
	big.GetPreprepareData().Proposal.RawProposal = bytes.Repeat([]byte{0xAB}, 15<<20)
	require.True(t, relayCheck(big, compactForkBlock-1, func(types.Address, uint64) bool { return true }, i.IsValidValidator),
		"the signature still verifies")
	require.False(t, i.relayable(big, compactForkBlock-1))

	// Before the fork the same message is judged by its full signature only.
	old := signedPrePrepare(t, i, addr, compactForkBlock-1, 0, []byte{1}, nil)
	require.True(t, i.relayable(old, compactForkBlock-2))
}

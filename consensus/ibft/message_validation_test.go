package ibft

import (
	"testing"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/stretchr/testify/require"
)

var (
	testFrom = make([]byte, 20)
	testSig  = []byte{1}
	testView = &proto.View{Height: 10, Round: 1}
)

func prepareMsg() *proto.Message {
	return &proto.Message{
		View: testView, From: testFrom, Signature: testSig, Type: proto.MessageType_PREPARE,
		Payload: &proto.Message_PrepareData{PrepareData: &proto.PrepareMessage{ProposalHash: []byte{1}}},
	}
}

func preprepareMsg(rcc *proto.RoundChangeCertificate) *proto.Message {
	return &proto.Message{
		View: testView, From: testFrom, Signature: testSig, Type: proto.MessageType_PREPREPARE,
		Payload: &proto.Message_PreprepareData{PreprepareData: &proto.PrePrepareMessage{
			Proposal:     &proto.Proposal{RawProposal: []byte{1}, Round: 1},
			ProposalHash: []byte{1},
			Certificate:  rcc,
		}},
	}
}

func roundChangeMsg(pc *proto.PreparedCertificate) *proto.Message {
	return &proto.Message{
		View: testView, From: testFrom, Signature: testSig, Type: proto.MessageType_ROUND_CHANGE,
		Payload: &proto.Message_RoundChangeData{RoundChangeData: &proto.RoundChangeMessage{
			LatestPreparedCertificate: pc,
		}},
	}
}

func commitMsg() *proto.Message {
	return &proto.Message{
		View: testView, From: testFrom, Signature: testSig, Type: proto.MessageType_COMMIT,
		Payload: &proto.Message_CommitData{CommitData: &proto.CommitMessage{
			ProposalHash: []byte{1}, CommittedSeal: []byte{1},
		}},
	}
}

func TestValidateIBFTMessage_Honest(t *testing.T) {
	t.Parallel()

	pc := &proto.PreparedCertificate{
		ProposalMessage: preprepareMsg(nil),
		PrepareMessages: []*proto.Message{prepareMsg(), prepareMsg()},
	}
	rcc := &proto.RoundChangeCertificate{
		RoundChangeMessages: []*proto.Message{roundChangeMsg(nil), roundChangeMsg(pc)},
	}

	for name, msg := range map[string]*proto.Message{
		"preprepare round 0":          preprepareMsg(nil),
		"preprepare with certificate": preprepareMsg(rcc),
		"prepare":                     prepareMsg(),
		"commit":                      commitMsg(),
		"round change empty":          roundChangeMsg(nil),
		"round change with PC":        roundChangeMsg(pc),
	} {
		require.NoError(t, validateIBFTMessage(msg), name)
	}
}

func TestValidateIBFTMessage_Malformed(t *testing.T) {
	t.Parallel()

	nilViewPrepare := prepareMsg()
	nilViewPrepare.View = nil

	cases := map[string]struct {
		msg  func() *proto.Message
		want error
	}{
		// C1: the proven chain-halt message.
		"nil message": {func() *proto.Message { return nil }, errNilMessage},
		"nil view": {func() *proto.Message {
			m := prepareMsg()
			m.View = nil

			return m
		}, errNilView},
		"short sender": {func() *proto.Message {
			m := prepareMsg()
			m.From = []byte{1}

			return m
		}, errInvalidSender},
		"no signature": {func() *proto.Message {
			m := prepareMsg()
			m.Signature = nil

			return m
		}, errMissingSignature},
		// H2 (a): unknown type hits a nil mutex in the go-ibft message store.
		"unknown type": {func() *proto.Message {
			m := prepareMsg()
			m.Type = 99

			return m
		}, errUnknownMessageType},
		"type does not match payload": {func() *proto.Message {
			m := prepareMsg()
			m.Type = proto.MessageType_COMMIT

			return m
		}, errPayloadMismatch},
		"nil payload data": {func() *proto.Message {
			m := commitMsg()
			m.Payload = &proto.Message_CommitData{}

			return m
		}, errPayloadMismatch},
		// H2 (b): PREPREPARE with no proposal is dereferenced before IsProposer.
		"preprepare without proposal": {func() *proto.Message {
			m := preprepareMsg(nil)
			m.GetPreprepareData().Proposal = nil

			return m
		}, errMissingProposal},
		"preprepare without hash": {func() *proto.Message {
			m := preprepareMsg(nil)
			m.GetPreprepareData().ProposalHash = nil

			return m
		}, errMissingHash},
		// H2 (c): nil-view prepares inside a prepared certificate.
		"round change with nil-view prepare in PC": {func() *proto.Message {
			return roundChangeMsg(&proto.PreparedCertificate{
				ProposalMessage: preprepareMsg(nil),
				PrepareMessages: []*proto.Message{nilViewPrepare},
			})
		}, errNilView},
		"round change with nil prepare in PC": {func() *proto.Message {
			return roundChangeMsg(&proto.PreparedCertificate{
				ProposalMessage: preprepareMsg(nil),
				PrepareMessages: []*proto.Message{nil},
			})
		}, errNilMessage},
		"PC proposal message of wrong type": {func() *proto.Message {
			return roundChangeMsg(&proto.PreparedCertificate{ProposalMessage: prepareMsg()})
		}, errPayloadMismatch},
		"certificate with nil round change": {func() *proto.Message {
			return preprepareMsg(&proto.RoundChangeCertificate{RoundChangeMessages: []*proto.Message{nil}})
		}, errNilMessage},
		"certificate with nil-view round change": {func() *proto.Message {
			rc := roundChangeMsg(nil)
			rc.View = nil

			return preprepareMsg(&proto.RoundChangeCertificate{RoundChangeMessages: []*proto.Message{rc}})
		}, errNilView},
	}

	for name, tc := range cases {
		require.ErrorIs(t, validateIBFTMessage(tc.msg()), tc.want, name)
	}
}

func TestValidateIBFTMessage_DepthLimit(t *testing.T) {
	t.Parallel()

	// Build PREPREPARE -> RCC -> RC -> PC -> PREPREPARE -> ... deeper than allowed.
	msg := preprepareMsg(nil)

	// Ten prepared round changes at one height must still be accepted.
	for i := 0; i < 10; i++ {
		rc := roundChangeMsg(&proto.PreparedCertificate{ProposalMessage: msg})
		msg = preprepareMsg(&proto.RoundChangeCertificate{RoundChangeMessages: []*proto.Message{rc}})
	}

	require.NoError(t, validateIBFTMessage(msg))

	for i := 0; i < maxCertificateDepth; i++ {
		rc := roundChangeMsg(&proto.PreparedCertificate{ProposalMessage: msg})
		msg = preprepareMsg(&proto.RoundChangeCertificate{RoundChangeMessages: []*proto.Message{rc}})
	}

	require.ErrorIs(t, validateIBFTMessage(msg), errNestedTooDeep)
}

// C1: IsValidValidator is called by go-ibft before its own nil check.
func TestIsValidValidator_NilView(t *testing.T) {
	t.Parallel()

	i := &backendIBFT{}

	require.False(t, i.IsValidValidator(nil))
	require.False(t, i.IsValidValidator(&proto.Message{From: testFrom, Signature: testSig}))
}

package ibft

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/stretchr/testify/require"
	gproto "google.golang.org/protobuf/proto"
)

// Consensus messages are signed over their protobuf encoding, so a dependency
// upgrade that changed the bytes would split old and new validators. The golden
// file was produced by v1.0.7's protobuf (v1.34.0).
func TestIBFTWireEncodingUnchanged(t *testing.T) {
	t.Parallel()

	golden, err := os.ReadFile("testdata/ibft_wire_protobuf_v1.34.txt")
	require.NoError(t, err)

	from := make([]byte, 20)
	for i := range from {
		from[i] = byte(i + 1)
	}
	view := &proto.View{Height: 23616601, Round: 3}
	pp := &proto.Message{View: view, From: from, Signature: []byte{9, 9}, Type: proto.MessageType_PREPREPARE,
		Payload: &proto.Message_PreprepareData{PreprepareData: &proto.PrePrepareMessage{
			Proposal: &proto.Proposal{RawProposal: []byte("block-rlp-bytes"), Round: 3}, ProposalHash: []byte{0xaa, 0xbb}}}}
	pr := &proto.Message{View: view, From: from, Signature: []byte{7}, Type: proto.MessageType_PREPARE,
		Payload: &proto.Message_PrepareData{PrepareData: &proto.PrepareMessage{ProposalHash: []byte{0xaa, 0xbb}}}}
	cm := &proto.Message{View: view, From: from, Signature: []byte{7}, Type: proto.MessageType_COMMIT,
		Payload: &proto.Message_CommitData{CommitData: &proto.CommitMessage{ProposalHash: []byte{0xaa}, CommittedSeal: []byte{1, 2, 3}}}}
	rc := &proto.Message{View: view, From: from, Signature: []byte{7}, Type: proto.MessageType_ROUND_CHANGE,
		Payload: &proto.Message_RoundChangeData{RoundChangeData: &proto.RoundChangeMessage{
			LastPreparedProposal:      &proto.Proposal{RawProposal: []byte{5}, Round: 2},
			LatestPreparedCertificate: &proto.PreparedCertificate{ProposalMessage: pp, PrepareMessages: []*proto.Message{pr, pr}}}}}
	ppc := &proto.Message{View: view, From: from, Signature: []byte{9}, Type: proto.MessageType_PREPREPARE,
		Payload: &proto.Message_PreprepareData{PreprepareData: &proto.PrePrepareMessage{
			Proposal: &proto.Proposal{RawProposal: []byte{1}, Round: 3}, ProposalHash: []byte{1},
			Certificate: &proto.RoundChangeCertificate{RoundChangeMessages: []*proto.Message{rc}}}}}

	var got strings.Builder

	for _, m := range []*proto.Message{pp, pr, cm, rc, ppc} {
		full, err := gproto.Marshal(m)
		require.NoError(t, err)

		nosig, err := m.PayloadNoSig()
		require.NoError(t, err)

		fmt.Fprintf(&got, "%s %s %s\n", m.Type, hex.EncodeToString(full), hex.EncodeToString(nosig))
	}

	require.Equal(t, string(golden), got.String())
}

package ibft

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	protoIBFT "github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func readObserved(t *testing.T, path string) []ObservedMessage {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)

	defer f.Close()

	var recs []ObservedMessage

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)

	for sc.Scan() {
		var r ObservedMessage
		require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
		recs = append(recs, r)
	}

	return recs
}

// Audit review H1: after WChainV111 the block in a round change is unsigned. A
// relayer's copy with another block must not become a second record with a
// different hash (a forged double-sign proof). A real second round change for
// the same view with a different signed claim still must.
func TestObserver_RoundChangeRecordsSignedClaim(t *testing.T) {
	i, addr := compactTestBackend(t)

	path := filepath.Join(t.TempDir(), "observed.jsonl")
	obs, closeFn, err := NewFileObserver(path)
	require.NoError(t, err)

	i.observer = obs

	block := bytes.Repeat([]byte{7}, 1000)
	pp := signedPrePrepare(t, i, addr, compactForkBlock, 0, block, nil)
	honest := signedRoundChange(t, i, addr, compactForkBlock, 1, pp, block)

	forged := proto.Clone(honest).(*protoIBFT.Message)
	forged.GetRoundChangeData().LastPreparedProposal.RawProposal = []byte("anything")
	require.True(t, i.IsValidValidator(forged))

	require.Equal(t, proposalHash(honest), proposalHash(forged), "the recorded hash is the signed claim")

	i.observe(honest, peer.ID("honest-peer"))
	i.observe(forged, peer.ID("attacker"))
	i.observe(honest, peer.ID("replayer"))

	// The same validator really signing a different claim for the same view:
	// a prepared block of another round.
	pp2 := signedPrePrepare(t, i, addr, compactForkBlock, 0, bytes.Repeat([]byte{8}, 1000), nil)
	pp2.GetPreprepareData().ProposalHash = bytes.Repeat([]byte{9}, 32)
	pp2 = i.signMessage(pp2)
	other := signedRoundChange(t, i, addr, compactForkBlock, 1, pp2, bytes.Repeat([]byte{8}, 1000))
	i.observe(other, peer.ID("honest-peer"))

	require.NoError(t, closeFn())

	recs := readObserved(t, path)
	require.Len(t, recs, 2, "copies with the same signature are recorded once")
	require.Equal(t, honest.GetRoundChangeData().LatestPreparedCertificate.ProposalMessage.GetPreprepareData().ProposalHash,
		pp.GetPreprepareData().ProposalHash)
	require.NotEqual(t, recs[0].ProposalHash, recs[1].ProposalHash, "a really conflicting claim is still a conflict")
	require.Equal(t, recs[0].Round, recs[1].Round)
}
